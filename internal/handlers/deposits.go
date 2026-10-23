package handlers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// Kautionen getrennt führen (Ausbau 32). Eine Kaution ist kein Erlös, solange sie nicht
// einbehalten wird: erhalten und zurückgezahlt stehen nur im Kautionskonto, nicht bei
// den Zahlungen, also weder im Umsatz noch in der Steuerauswertung. Einbehalten bucht
// den Betrag über den normalen Zahlungsweg (pay-invoices: offene Rechnungen zuerst, der
// Rest wird Guthaben) — erst damit ist er Erlös.

type deposit struct {
	ID        int64     `json:"id"`
	PersonID  int64     `json:"person_id"`
	VehicleID *int64    `json:"vehicle_id"`
	Kind      string    `json:"kind"`
	Amount    float64   `json:"amount"`
	BookedOn  time.Time `json:"booked_on"`
	Method    string    `json:"method"`
	Note      string    `json:"note"`
	PaymentID *int64    `json:"payment_id"`
	Reversed  bool      `json:"reversed"`
	CreatedAt time.Time `json:"created_at"`
}

var depositKindLabel = map[string]string{"erhalten": "Kaution erhalten", "einbehalten": "Kaution einbehalten", "zurueck": "Kaution zurückgezahlt"}

// depositBalanceCents: erhalten minus einbehalten minus zurückgezahlt, in Cent.
func depositBalanceCents(ctx context.Context, q dbQuerier, personID int64) (int64, error) {
	var cents int64
	err := q.QueryRow(ctx,
		`SELECT COALESCE(sum(CASE d.kind WHEN 'erhalten' THEN d.amount ELSE -d.amount END) * 100, 0)::bigint
		   FROM deposits d LEFT JOIN payments p ON p.id = d.payment_id
		  WHERE d.person_id = $1 AND NOT COALESCE(p.reversed, false)`, personID).Scan(&cents)
	return cents, err
}

// ListDeposits: GET /api/persons/{id}/deposits → {balance, entries}.
func (h *Handler) ListDeposits(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT d.id, d.person_id, d.vehicle_id, d.kind, d.amount, d.booked_on, d.method, d.note,
		        d.payment_id, d.created_at, COALESCE(p.reversed, false)
		   FROM deposits d LEFT JOIN payments p ON p.id = d.payment_id
		  WHERE d.person_id = $1 ORDER BY d.booked_on, d.id`, id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []deposit{}
	var cents int64
	for rows.Next() {
		var d deposit
		if err := rows.Scan(&d.ID, &d.PersonID, &d.VehicleID, &d.Kind, &d.Amount, &d.BookedOn, &d.Method, &d.Note, &d.PaymentID, &d.CreatedAt, &d.Reversed); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		if !d.Reversed {
			if d.Kind == "erhalten" {
				cents += toCentsH(d.Amount)
			} else {
				cents -= toCentsH(d.Amount)
			}
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balance": float64(cents) / 100, "entries": out})
}

func toCentsH(v float64) int64 { return int64(math.Round(v * 100)) }

// CreateDeposit: POST /api/persons/{id}/deposits {kind, amount, booked_on?, method?, note?, vehicle_id?}.
func (h *Handler) CreateDeposit(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Kind      string  `json:"kind"`
		Amount    float64 `json:"amount"`
		BookedOn  *string `json:"booked_on"`
		Method    string  `json:"method"`
		Note      string  `json:"note"`
		VehicleID *int64  `json:"vehicle_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.Note, in.Method = trim(in.Note), trim(in.Method)
	if _, ok := depositKindLabel[in.Kind]; !ok {
		writeError(w, http.StatusBadRequest, "Unbekannte Buchungsart")
		return
	}
	if in.Amount <= 0 || in.Amount > maxMoneyAmount || round2(in.Amount) != in.Amount {
		writeError(w, http.StatusBadRequest, "Betrag muss größer als 0 sein (auf den Cent)")
		return
	}
	if !validNoteLength(in.Note) || len([]rune(in.Method)) > 40 {
		writeError(w, http.StatusBadRequest, "Eingabe ist zu lang")
		return
	}
	if in.Kind == "einbehalten" && in.Note == "" {
		writeError(w, http.StatusBadRequest, "Für das Einbehalten ist ein Grund nötig")
		return
	}
	booked := startOfDayUTC(h.now())
	if in.BookedOn != nil && trim(*in.BookedOn) != "" {
		d, err := parseOptDate(in.BookedOn)
		if err != nil || d == nil {
			writeError(w, http.StatusBadRequest, "booked_on must be YYYY-MM-DD")
			return
		}
		if d.After(booked) {
			writeError(w, http.StatusBadRequest, "Buchungen liegen nicht in der Zukunft")
			return
		}
		booked = *d
	}
	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}
	// Saldo prüfen und buchen unter der Personensperre: zwei gleichzeitige Rückzahlungen
	// können die Kaution nicht zweimal auszahlen.
	var depID int64
	var paymentID *int64
	var tooMuch, notFound bool
	err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := lockInvoicePersonTx(r.Context(), tx, pid); err != nil {
			return err
		}
		var anonymized bool
		if err := tx.QueryRow(r.Context(), `SELECT anonymized FROM persons WHERE id = $1 FOR UPDATE`, pid).Scan(&anonymized); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = true
				return nil
			}
			return err
		}
		if in.VehicleID != nil {
			var owner int64
			err := tx.QueryRow(r.Context(), `SELECT person_id FROM vehicles WHERE id = $1`, *in.VehicleID).Scan(&owner)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if errors.Is(err, pgx.ErrNoRows) || owner != pid {
				notFound = true
				return nil
			}
		}
		if in.Kind != "erhalten" {
			bal, err := depositBalanceCents(r.Context(), tx, pid)
			if err != nil {
				return err
			}
			if toCentsH(in.Amount) > bal {
				tooMuch = true
				return nil
			}
		}
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO deposits (person_id, vehicle_id, kind, amount, booked_on, method, note, created_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			pid, in.VehicleID, in.Kind, in.Amount, booked, in.Method, in.Note, createdBy).Scan(&depID); err != nil {
			return err
		}
		if in.Kind == "einbehalten" {
			prepared, err := h.prepareInvoicePayment(r, pid, payInvoicesRequest{
				Amount: in.Amount, PaidOn: booked.Format(dateLayout), Method: "sonstiges",
				Note: "Kaution einbehalten: " + in.Note, Auto: true,
			}, "deposit-"+strconv.FormatInt(depID, 10))
			if err != nil {
				return err
			}
			out, _, err := h.recordInvoicePaymentTx(r, tx, prepared)
			if err != nil {
				return err
			}
			paymentID = &out.PaymentID
			if _, err := tx.Exec(r.Context(), `UPDATE deposits SET payment_id = $2 WHERE id = $1`, depID, out.PaymentID); err != nil {
				return err
			}
		}
		return h.auditCreatedTx(r.Context(), tx, r, "deposit", depID,
			depositKindLabel[in.Kind]+": "+strconv.FormatFloat(in.Amount, 'f', 2, 64)+" €", map[string]any{
				"person_id": pid, "kind": in.Kind, "amount": in.Amount, "booked_on": booked.Format(dateLayout), "payment_id": paymentID,
			})
	})
	if writeInvoicePaymentError(w, r, err) {
		return
	}
	switch {
	case notFound:
		writeError(w, http.StatusNotFound, "Person oder Gefährt nicht gefunden")
		return
	case tooMuch:
		writeError(w, http.StatusConflict, "Mehr als die offene Kaution")
		return
	}
	if in.Kind == "einbehalten" {
		h.archiveInvoiceSettledCollected(r.Context(), pid)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": depID, "payment_id": paymentID})
}

// DeleteDeposit: DELETE /api/deposits/{id} (Admin). Ein Einbehalt bleibt als Beleg
// erhalten und wird über die Zahlung storniert. Löschen darf den Saldo nicht negativ machen.
func (h *Handler) DeleteDeposit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var d deposit
	var blocked string
	err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(),
			`SELECT person_id, kind, amount, booked_on, payment_id FROM deposits WHERE id = $1`, id).
			Scan(&d.PersonID, &d.Kind, &d.Amount, &d.BookedOn, &d.PaymentID); err != nil {
			return err
		}
		if err := lockInvoicePersonTx(r.Context(), tx, d.PersonID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `SELECT 1 FROM persons WHERE id = $1 FOR UPDATE`, d.PersonID); err != nil {
			return err
		}
		if err := tx.QueryRow(r.Context(),
			`SELECT person_id, kind, amount, booked_on, payment_id FROM deposits WHERE id = $1 FOR UPDATE`, id).
			Scan(&d.PersonID, &d.Kind, &d.Amount, &d.BookedOn, &d.PaymentID); err != nil {
			return err
		}
		if d.PaymentID != nil {
			blocked = "Der Einbehalt bleibt als Beleg erhalten. Die zugehörige Zahlung kann storniert werden."
			return nil
		}
		if d.Kind == "erhalten" {
			bal, err := depositBalanceCents(r.Context(), tx, d.PersonID)
			if err != nil {
				return err
			}
			if bal-toCentsH(d.Amount) < 0 {
				blocked = "Davon wurde schon zurückgezahlt oder einbehalten – zuerst diese Buchung löschen."
				return nil
			}
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM deposits WHERE id = $1`, id); err != nil {
			return err
		}
		return h.auditDeletedTx(r.Context(), tx, r, "deposit", id, depositKindLabel[d.Kind]+" gelöscht", map[string]any{
			"person_id": d.PersonID, "kind": d.Kind, "amount": d.Amount, "booked_on": d.BookedOn.Format(dateLayout),
		})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "not found")
		return
	case err != nil:
		serverError(w, r, "could not delete", err)
		return
	case blocked != "":
		writeError(w, http.StatusConflict, blocked)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
