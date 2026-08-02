package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// payment is one recorded money-in entry (see migration 023_payments.sql).
type payment struct {
	ID        int64     `json:"id"`
	PersonID  int64     `json:"person_id"`
	Amount    float64   `json:"amount"`
	PaidOn    time.Time `json:"paid_on"`
	Method    string    `json:"method"`
	Note      string    `json:"note"`
	VehicleID *int64    `json:"vehicle_id"`
	CreatedAt time.Time `json:"created_at"`
}

// paymentMethods is the closed set the UI offers; anything else is rejected so a
// typo can't create an unfilterable method.
var paymentMethods = map[string]bool{"bar": true, "ueberweisung": true, "paypal": true, "sonstiges": true}

const paymentColumns = `id, person_id, amount, paid_on, method, note, vehicle_id, created_at`

func scanPayment(row pgx.Row) (payment, error) {
	var p payment
	err := row.Scan(&p.ID, &p.PersonID, &p.Amount, &p.PaidOn, &p.Method, &p.Note, &p.VehicleID, &p.CreatedAt)
	return p, err
}

// ListPayments returns a person's recorded payments, newest first.
func (h *Handler) ListPayments(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT `+paymentColumns+` FROM payments WHERE person_id=$1 ORDER BY paid_on DESC, id DESC`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []payment{}
	for rows.Next() {
		p, serr := scanPayment(rows)
		if serr != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		out = append(out, p)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type paymentRequest struct {
	Amount    float64 `json:"amount"`
	PaidOn    string  `json:"paid_on"`
	Method    string  `json:"method"`
	Note      string  `json:"note"`
	VehicleID *int64  `json:"vehicle_id"`
	// Allocate, when true, marks the person's open individually-owed items paid,
	// oldest first, consuming up to Amount (Phase-2 FIFO settlement).
	Allocate bool `json:"allocate"`
}

// allocateToOpenItems marks a person's open, individually-owed items paid oldest
// first, consuming up to `amount`. It settles standalone one-off charges and
// uncovered vehicles (whole items only — no partial); Pauschale/recurring
// periods keep their own sliders. Reuses the same paid updates as the manual
// toggles, so the (toggle-based) balance stays the single source of truth.
// Returns the number of items settled.
func (h *Handler) allocateToOpenItems(r *http.Request, personID int64, amount float64) (int, error) {
	ctx := r.Context()
	now := time.Now()

	// Vehicles bound to a Pauschale settle via that Pauschale — never here.
	ags, err := h.loadAgreements(ctx, personID, now)
	if err != nil {
		return 0, err
	}
	covered := map[int64]bool{}
	for i := range ags {
		for _, vid := range ags[i].VehicleIDs {
			covered[vid] = true
		}
	}

	type owedItem struct {
		date time.Time
		owed float64
		kind string // "vehicle" | "charge"
		id   int64
	}
	var items []owedItem

	vehicles, _, err := h.loadVehiclesWithCategories(r, personID)
	if err != nil {
		return 0, err
	}
	for i := range vehicles {
		v := &vehicles[i]
		if v.Paid || v.Archived || covered[v.ID] || v.AccruedCost <= 0.005 {
			continue
		}
		items = append(items, owedItem{v.StartDate, v.AccruedCost, "vehicle", v.ID})
	}

	rows, err := h.Pool.Query(ctx,
		`SELECT id, amount*quantity, charged_on FROM charges
		   WHERE person_id=$1 AND vehicle_id IS NULL AND NOT paid`, personID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var it owedItem
		it.kind = "charge"
		if err := rows.Scan(&it.id, &it.owed, &it.date); err != nil {
			return 0, err
		}
		if it.owed > 0.005 {
			items = append(items, it)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	sort.Slice(items, func(i, j int) bool { return items[i].date.Before(items[j].date) })

	remaining := amount
	settled := 0
	for _, it := range items {
		if remaining+0.005 < it.owed { // strict oldest-first: stop at the first item that doesn't fit
			break
		}
		switch it.kind {
		case "vehicle":
			if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET paid=true, updated_at=now() WHERE id=$1`, it.id); err != nil {
				return settled, err
			}
			h.autoArchiveIfClosed(r, it.id)
		case "charge":
			if _, err := h.Pool.Exec(ctx, `UPDATE charges SET paid=true WHERE id=$1 AND vehicle_id IS NULL`, it.id); err != nil {
				return settled, err
			}
		}
		remaining -= it.owed
		settled++
	}
	return settled, nil
}

// validatePayment normalizes and checks a payment request, returning the parsed
// date. A non-empty badMsg is a 400 reason; a non-nil error is a 500. A bound
// vehicle (optional context) must belong to the same person.
func (h *Handler) validatePayment(ctx context.Context, personID int64, req *paymentRequest) (time.Time, string, error) {
	req.Method = trim(req.Method)
	req.Note = trim(req.Note)
	if req.Method == "" {
		req.Method = "bar"
	}
	if !paymentMethods[req.Method] {
		return time.Time{}, "unknown payment method", nil
	}
	if req.Amount <= 0 {
		return time.Time{}, "amount must be greater than 0", nil
	}
	if !validNameLength(req.Note) {
		return time.Time{}, "note is too long", nil
	}
	if req.VehicleID != nil {
		var owner int64
		err := h.Pool.QueryRow(ctx, `SELECT person_id FROM vehicles WHERE id=$1`, *req.VehicleID).Scan(&owner)
		if err == pgx.ErrNoRows || (err == nil && owner != personID) {
			return time.Time{}, "vehicle does not belong to that person", nil
		}
		if err != nil {
			return time.Time{}, "", err
		}
	}
	paidOn := time.Now()
	if trim(req.PaidOn) != "" {
		t, perr := time.Parse(dateLayout, trim(req.PaidOn))
		if perr != nil {
			return time.Time{}, "paid_on must be YYYY-MM-DD", nil
		}
		paidOn = t
	}
	return paidOn, "", nil
}

// CreatePayment records a payment for a person (editor role).
func (h *Handler) CreatePayment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req paymentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	paidOn, badMsg, serr := h.validatePayment(r.Context(), id, &req)
	if serr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if badMsg != "" {
		writeError(w, http.StatusBadRequest, badMsg)
		return
	}
	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}
	p, err := scanPayment(h.Pool.QueryRow(r.Context(),
		`INSERT INTO payments (person_id, amount, paid_on, method, note, vehicle_id, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+paymentColumns,
		id, req.Amount, paidOn, req.Method, req.Note, req.VehicleID, createdBy))
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "person or vehicle does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not record payment")
		return
	}
	h.audit(r, "create", "payment", p.ID, fmt.Sprintf("recorded payment %.2f € (%s)", p.Amount, p.Method))

	settled := 0
	if req.Allocate {
		if n, aerr := h.allocateToOpenItems(r, id, req.Amount); aerr != nil {
			slog.Warn("payment allocation failed", "payment_id", p.ID, "err", aerr)
		} else if n > 0 {
			settled = n
			h.audit(r, "update", "payment", p.ID, fmt.Sprintf("allocated payment to %d open item(s), oldest first", n))
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": p.ID, "person_id": p.PersonID, "amount": p.Amount, "paid_on": p.PaidOn,
		"method": p.Method, "note": p.Note, "vehicle_id": p.VehicleID, "created_at": p.CreatedAt,
		"settled": settled,
	})
}

// DeletePayment removes a recorded payment (editor role).
func (h *Handler) DeletePayment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM payments WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete payment")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "payment not found")
		return
	}
	h.audit(r, "delete", "payment", id, "deleted payment")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
