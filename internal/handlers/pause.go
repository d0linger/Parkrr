package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// Ruhezeit („Kurz weg“): ein eingelagertes Gefährt ist ein paar Wochen unterwegs und
// kommt zurück. Entscheidungen vom 16.10.2026: Platz halten (Standard) oder freigeben,
// keine Haltegebühr, gekürzt ab dem ersten Tag, unter einer Pauschale nur vermerkt,
// vorab bezahlt verschiebt „bezahlt bis“, gebundene Nebenkosten ruhen mit, Kunden
// fragen im Portal an. Die Abrechnung rechnet die Lücke über models.Pause (sliceCents);
// hier stehen die Regeln drumherum. Der Status bleibt „eingelagert“ — „unterwegs“ ist
// ein Zustand davon, keine vierte Stellung.

var openUntil = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

type vehiclePause struct {
	ID         int64      `json:"id"`
	VehicleID  int64      `json:"vehicle_id"`
	From       time.Time  `json:"from"`
	Until      *time.Time `json:"until"`
	Ended      bool       `json:"ended"`
	HoldSpot   bool       `json:"hold_spot"`
	PrevSpotID *int64     `json:"prev_spot_id"`
	Note       string     `json:"note"`
	Source     string     `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
	paidBefore *time.Time
}

const pauseCols = `id, vehicle_id, start_date, until, ended, hold_spot, prev_spot_id, note, source, created_at, paid_through_before`

func scanPause(row rowScanner) (vehiclePause, error) {
	var p vehiclePause
	err := row.Scan(&p.ID, &p.VehicleID, &p.From, &p.Until, &p.Ended, &p.HoldSpot, &p.PrevSpotID, &p.Note, &p.Source, &p.CreatedAt, &p.paidBefore)
	return p, err
}

// billingEnd: bis wann die Ruhezeit die Abrechnung aussetzt (offen, bis „Ist zurück“).
func (p vehiclePause) billingEnd() time.Time {
	if p.Ended && p.Until != nil {
		return *p.Until
	}
	return openUntil
}

// pauseLockedThrough: letzter Tag, den eine nicht stornierte Rechnung für die Miete
// des Gefährts oder seine gebundenen Nebenkosten schon abgerechnet hat. Eine Ruhezeit
// darf davor nichts ändern — eine gestellte Rechnung bleibt, wie sie ist.
func (h *Handler) pauseLockedThrough(ctx context.Context, q dbQuerier, vehicleID int64) (time.Time, error) {
	rows, err := q.Query(ctx,
		`SELECT s.period_key FROM invoice_source s JOIN invoices i ON i.id = s.invoice_id
		  WHERE NOT i.canceled AND s.period_key <> ''
		    AND ((s.kind = 'vehicle' AND s.ref_id = $1)
		      OR (s.kind = 'recurring' AND s.ref_id IN (SELECT id FROM recurring_charges WHERE vehicle_id = $1)))`, vehicleID)
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()
	var last time.Time
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return time.Time{}, err
		}
		if end := periodKeyEnd(key); !end.IsZero() {
			if d := end.AddDate(0, 0, -1); d.After(last) {
				last = d
			}
		}
	}
	return last, rows.Err()
}

func lockedMsg(locked time.Time) string {
	return "Bis " + locked.Format("02.01.2006") + " ist schon abgerechnet. Frühester möglicher Tag: " + locked.AddDate(0, 0, 1).Format("02.01.2006") + "."
}

// shiftPaidThrough: vorab bezahlte Ruhetage verschieben „bezahlt bis“ um ebenso viele
// Miettage nach hinten (Ruhetage zählen dabei nicht). Kein Geld fließt.
func shiftPaidThrough(paidThrough, from, until time.Time, end *time.Time, paused func(time.Time) bool) time.Time {
	overlapEnd := paidThrough
	if until.Before(overlapEnd) {
		overlapEnd = until
	}
	if overlapEnd.Before(from) {
		return paidThrough
	}
	days := int(overlapEnd.Sub(from).Hours()/24) + 1
	d := paidThrough
	for days > 0 {
		d = d.AddDate(0, 0, 1)
		if end != nil && d.After(*end) {
			return *end
		}
		if paused(d) {
			continue
		}
		days--
	}
	return d
}

func (h *Handler) loadPause(ctx context.Context, q dbQuerier, id int64) (vehiclePause, error) {
	return scanPause(q.QueryRow(ctx, `SELECT `+pauseCols+` FROM vehicle_pauses WHERE id = $1`, id))
}

func (h *Handler) listPauses(ctx context.Context, q dbQuerier, vehicleID int64) ([]vehiclePause, error) {
	rows, err := q.Query(ctx, `SELECT `+pauseCols+` FROM vehicle_pauses WHERE vehicle_id = $1 ORDER BY start_date`, vehicleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []vehiclePause{}
	for rows.Next() {
		p, err := scanPause(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// VehiclePauses: GET /api/vehicles/{id}/pauses.
func (h *Handler) VehiclePauses(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	out, err := h.listPauses(r.Context(), h.Pool, id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type pauseIn struct {
	From     *string `json:"from"`
	Until    *string `json:"until"`
	HoldSpot *bool   `json:"hold_spot"`
	Note     string  `json:"note"`
}

// errPauseRule trägt eine Regelverletzung mit Status und deutscher Meldung.
type errPauseRule struct {
	status int
	msg    string
}

func (e errPauseRule) Error() string { return e.msg }

// createPauseTx legt eine Ruhezeit an (gemeinsamer Weg für Betreiber und Portal-Übernahme).
func (h *Handler) createPauseTx(ctx context.Context, tx pgx.Tx, vehicleID int64, in pauseIn, source string, by *int64) (vehiclePause, error) {
	today := startOfDayUTC(h.now())
	from := today
	if in.From != nil && trim(*in.From) != "" {
		d, err := parseOptDate(in.From)
		if err != nil || d == nil {
			return vehiclePause{}, errPauseRule{http.StatusBadRequest, "Beginn als JJJJ-MM-TT"}
		}
		from = *d
	}
	var until *time.Time
	if in.Until != nil && trim(*in.Until) != "" {
		d, err := parseOptDate(in.Until)
		if err != nil || d == nil {
			return vehiclePause{}, errPauseRule{http.StatusBadRequest, "Rückkehr als JJJJ-MM-TT"}
		}
		if d.Before(from) {
			return vehiclePause{}, errPauseRule{http.StatusBadRequest, "Die Rückkehr liegt vor dem Beginn"}
		}
		until = d
	}
	hold := in.HoldSpot == nil || *in.HoldSpot
	note := trim(in.Note)
	if !validNoteLength(note) {
		return vehiclePause{}, errPauseRule{http.StatusBadRequest, "Notiz zu lang"}
	}
	var (
		status   string
		archived bool
		start    time.Time
		end      *time.Time
		spotID   *int64
	)
	if err := tx.QueryRow(ctx, `SELECT status, archived, start_date, end_date, spot_id FROM vehicles WHERE id = $1 FOR UPDATE`, vehicleID).
		Scan(&status, &archived, &start, &end, &spotID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return vehiclePause{}, errPauseRule{http.StatusNotFound, "Gefährt nicht gefunden"}
		}
		return vehiclePause{}, err
	}
	if archived || status != models.StatusStored {
		return vehiclePause{}, errPauseRule{http.StatusConflict, "Nur ein eingelagertes Gefährt kann kurz weg sein"}
	}
	if from.Before(start) || (end != nil && from.After(*end)) {
		return vehiclePause{}, errPauseRule{http.StatusBadRequest, "Die Ruhezeit muss in der Einlagerung liegen"}
	}
	if !hold && from.After(today) {
		return vehiclePause{}, errPauseRule{http.StatusBadRequest, "Den Platz freigeben geht erst am Abfahrtstag"}
	}
	locked, err := h.pauseLockedThrough(ctx, tx, vehicleID)
	if err != nil {
		return vehiclePause{}, err
	}
	if !from.After(locked) && !locked.IsZero() {
		return vehiclePause{}, errPauseRule{http.StatusConflict, lockedMsg(locked)}
	}
	var overlap bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM vehicle_pauses WHERE vehicle_id = $1
		    AND start_date <= COALESCE($3::date, 'infinity'::date)
		    AND (CASE WHEN ended THEN until ELSE 'infinity'::date END) >= $2)`, vehicleID, from, until).Scan(&overlap); err != nil {
		return vehiclePause{}, err
	}
	if overlap {
		return vehiclePause{}, errPauseRule{http.StatusConflict, "Überschneidet sich mit einer anderen Ruhezeit"}
	}
	var prev *int64
	if !hold && spotID != nil {
		prev = spotID
		if _, err := tx.Exec(ctx, `UPDATE vehicles SET spot_id = NULL, updated_at = now() WHERE id = $1`, vehicleID); err != nil {
			return vehiclePause{}, err
		}
	}
	return scanPause(tx.QueryRow(ctx,
		`INSERT INTO vehicle_pauses (vehicle_id, start_date, until, hold_spot, prev_spot_id, note, source, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+pauseCols,
		vehicleID, from, until, hold, prev, note, source, by))
}

func writePauseErr(w http.ResponseWriter, r *http.Request, err error) {
	var rule errPauseRule
	if errors.As(err, &rule) {
		writeError(w, rule.status, rule.msg)
		return
	}
	serverError(w, r, "could not save pause", err)
}

func actor(r *http.Request) *int64 {
	if u, ok := auth.UserFrom(r.Context()); ok && u != nil {
		return &u.ID
	}
	return nil
}

// StartPause: POST /api/vehicles/{id}/pause {from?, until?, hold_spot?, note?}.
func (h *Handler) StartPause(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in pauseIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var p vehiclePause
	err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		p, err = h.createPauseTx(r.Context(), tx, id, in, "betreiber", actor(r))
		return err
	})
	if err != nil {
		writePauseErr(w, r, err)
		return
	}
	h.auditCreated(r, "vehicle_pause", p.ID, "Ruhezeit ab "+p.From.Format("02.01.2006")+" eingetragen", map[string]any{
		"vehicle_id": id, "from": p.From.Format(dateLayout), "until": dateOrNil(p.Until), "hold_spot": p.HoldSpot,
	})
	writeJSON(w, http.StatusCreated, p)
}

// ExtendPause: PUT /api/vehicle-pauses/{id} {until} — geplante Rückkehr ändern (+1 Woche).
// Solange die Ruhezeit läuft, ändert das an der Abrechnung nichts.
func (h *Handler) ExtendPause(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Until *string `json:"until"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var until *time.Time
	if in.Until != nil && trim(*in.Until) != "" {
		d, err := parseOptDate(in.Until)
		if err != nil || d == nil {
			writeError(w, http.StatusBadRequest, "Rückkehr als JJJJ-MM-TT")
			return
		}
		until = d
	}
	p, err := h.loadPause(r.Context(), h.Pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if p.Ended {
		writeError(w, http.StatusConflict, "Die Ruhezeit ist schon beendet")
		return
	}
	if until != nil && until.Before(p.From) {
		writeError(w, http.StatusBadRequest, "Die Rückkehr liegt vor dem Beginn")
		return
	}
	if _, err := h.Pool.Exec(r.Context(), `UPDATE vehicle_pauses SET until = $2, updated_at = now() WHERE id = $1`, id, until); err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	h.auditChange(r, "update", "vehicle_pause", id, "Geplante Rückkehr geändert",
		diffFields(map[string]any{"until": dateOrNil(p.Until)}, map[string]any{"until": dateOrNil(until)}))
	p.Until = until
	writeJSON(w, http.StatusOK, p)
}

// ReturnFromPause: POST /api/vehicle-pauses/{id}/return {on?} — „Ist zurück“. Der
// Rückkehrtag zählt wieder als Miettag; die Ruhezeit endet am Tag davor. War die Miete
// vorab bezahlt, verschiebt sich „bezahlt bis“ um die Ruhetage. Ein freigegebener Platz
// wird wieder belegt, wenn er noch frei ist.
func (h *Handler) ReturnFromPause(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		On *string `json:"on"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	on := startOfDayUTC(h.now())
	if in.On != nil && trim(*in.On) != "" {
		d, err := parseOptDate(in.On)
		if err != nil || d == nil {
			writeError(w, http.StatusBadRequest, "Rückkehrtag als JJJJ-MM-TT")
			return
		}
		on = *d
	}
	var p vehiclePause
	var removed, relinked bool
	var newPaid *time.Time
	err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if p, err = h.loadPause(r.Context(), tx, id); err != nil {
			return err
		}
		if p.Ended {
			return errPauseRule{http.StatusConflict, "Die Ruhezeit ist schon beendet"}
		}
		if on.Before(p.From) {
			return errPauseRule{http.StatusBadRequest, "Der Rückkehrtag liegt vor dem Beginn"}
		}
		var paidThrough, end *time.Time
		if err := tx.QueryRow(r.Context(), `SELECT paid_through, end_date FROM vehicles WHERE id = $1 FOR UPDATE`, p.VehicleID).Scan(&paidThrough, &end); err != nil {
			return err
		}
		locked, err := h.pauseLockedThrough(r.Context(), tx, p.VehicleID)
		if err != nil {
			return err
		}
		last := on.AddDate(0, 0, -1)
		if !locked.IsZero() && last.Before(locked) {
			return errPauseRule{http.StatusConflict, lockedMsg(locked)}
		}
		if on.Equal(p.From) {
			// Am selben Tag zurück: es gab keinen Ruhetag, die Ruhezeit entfällt.
			removed = true
			if _, err := tx.Exec(r.Context(), `DELETE FROM vehicle_pauses WHERE id = $1`, id); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(r.Context(), `UPDATE vehicle_pauses SET until = $2, ended = true, paid_through_before = $3, updated_at = now() WHERE id = $1`,
				id, last, paidThrough); err != nil {
				return err
			}
			if paidThrough != nil {
				others, err := h.listPauses(r.Context(), tx, p.VehicleID)
				if err != nil {
					return err
				}
				paused := func(d time.Time) bool {
					for _, o := range others {
						oe := o.billingEnd()
						if o.ID == id {
							oe = last
						}
						if !d.Before(o.From) && !d.After(oe) {
							return true
						}
					}
					return false
				}
				np := shiftPaidThrough(*paidThrough, p.From, last, end, paused)
				if !np.Equal(*paidThrough) {
					newPaid = &np
					if _, err := tx.Exec(r.Context(), `UPDATE vehicles SET paid_through = $2, updated_at = now() WHERE id = $1`, p.VehicleID, np); err != nil {
						return err
					}
				}
			}
		}
		if p.PrevSpotID != nil {
			ct, err := tx.Exec(r.Context(),
				`UPDATE vehicles SET spot_id = $2, updated_at = now() WHERE id = $1 AND spot_id IS NULL
				   AND NOT EXISTS (SELECT 1 FROM vehicles o WHERE o.spot_id = $2)`, p.VehicleID, *p.PrevSpotID)
			if err != nil {
				return err
			}
			relinked = ct.RowsAffected() == 1
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writePauseErr(w, r, err)
		return
	}
	if removed {
		h.auditDeleted(r, "vehicle_pause", id, "Ruhezeit entfällt (am selben Tag zurück)", map[string]any{
			"vehicle_id": p.VehicleID, "from": p.From.Format(dateLayout)})
	} else {
		spotAfter := any(nil)
		if relinked {
			spotAfter = *p.PrevSpotID
		}
		h.auditChange(r, "update", "vehicle_pause", id, "Ist zurück: Ruhezeit bis "+on.AddDate(0, 0, -1).Format("02.01.2006"),
			diffFields(map[string]any{"until": dateOrNil(p.Until), "ended": false, "paid_through_before": nil, "paid_through": nil, "spot_id": nil},
				map[string]any{"until": on.AddDate(0, 0, -1).Format(dateLayout), "ended": true, "paid_through_before": "gemerkt",
					"paid_through": dateOrNil(newPaid), "spot_id": spotAfter}))
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed, "relinked": relinked, "paid_through": newPaid,
		"prev_spot_id": p.PrevSpotID, "vehicle_id": p.VehicleID})
}

// CollectFromPause: POST /api/vehicle-pauses/{id}/collect — „Doch abgeholt“. Daraus
// wird ein normales Abholen am ersten Ruhetag (über den Statuswechsel), die Ruhezeit
// entfällt.
func (h *Handler) CollectFromPause(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	p, err := h.loadPause(r.Context(), h.Pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	locked, err := h.pauseLockedThrough(r.Context(), h.Pool, p.VehicleID)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if !locked.IsZero() && !p.From.After(locked) {
		writeError(w, http.StatusConflict, lockedMsg(locked)+" Bitte über Storno und Gutschrift korrigieren.")
		return
	}
	if _, err := h.Pool.Exec(r.Context(), `DELETE FROM vehicle_pauses WHERE id = $1`, id); err != nil {
		serverError(w, r, "could not delete", err)
		return
	}
	body, _ := json.Marshal(statusRequest{Status: models.StatusCollected, Date: p.From.Format(dateLayout), Note: "Doch abgeholt (Ruhezeit entfällt)"})
	vid := strconv.FormatInt(p.VehicleID, 10)
	sub := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+vid+"/status", bytes.NewReader(body)).WithContext(r.Context())
	sub.Header.Set("Content-Type", "application/json")
	sub.SetPathValue("id", vid)
	rec := httptest.NewRecorder()
	h.ChangeVehicleStatus(rec, sub)
	if rec.Code >= 300 {
		// Abholen gescheitert: die Ruhezeit kommt zurück, wie sie war.
		if _, rerr := h.Pool.Exec(r.Context(),
			`INSERT INTO vehicle_pauses (id, vehicle_id, start_date, until, ended, hold_spot, prev_spot_id, note, source, created_at, paid_through_before)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			p.ID, p.VehicleID, p.From, p.Until, p.Ended, p.HoldSpot, p.PrevSpotID, p.Note, p.Source, p.CreatedAt, p.paidBefore); rerr != nil {
			serverError(w, r, "could not restore pause", rerr)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	h.auditDeleted(r, "vehicle_pause", id, "Doch abgeholt: Ruhezeit entfällt, Abholung am "+p.From.Format("02.01.2006"),
		map[string]any{"vehicle_id": p.VehicleID, "from": p.From.Format(dateLayout), "until": dateOrNil(p.Until)})
	writeJSON(w, http.StatusOK, map[string]any{"collected": true, "end_date": p.From.Format(dateLayout)})
}

// DeletePause: DELETE /api/vehicle-pauses/{id} — Fehleintrag zurücknehmen. Nur ohne
// abgerechnete Tage; „bezahlt bis“ und ein freigegebener Platz kehren zurück.
func (h *Handler) DeletePause(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var p vehiclePause
	err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if p, err = h.loadPause(r.Context(), tx, id); err != nil {
			return err
		}
		locked, err := h.pauseLockedThrough(r.Context(), tx, p.VehicleID)
		if err != nil {
			return err
		}
		if !locked.IsZero() && !p.From.After(locked) {
			return errPauseRule{http.StatusConflict, lockedMsg(locked) + " Die Ruhezeit lässt sich nicht mehr entfernen."}
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM vehicle_pauses WHERE id = $1`, id); err != nil {
			return err
		}
		if p.Ended && p.paidBefore != nil {
			if _, err := tx.Exec(r.Context(), `UPDATE vehicles SET paid_through = $2, updated_at = now() WHERE id = $1`, p.VehicleID, *p.paidBefore); err != nil {
				return err
			}
		}
		if p.PrevSpotID != nil {
			if _, err := tx.Exec(r.Context(),
				`UPDATE vehicles SET spot_id = $2, updated_at = now() WHERE id = $1 AND spot_id IS NULL
				   AND NOT EXISTS (SELECT 1 FROM vehicles o WHERE o.spot_id = $2)`, p.VehicleID, *p.PrevSpotID); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writePauseErr(w, r, err)
		return
	}
	h.auditDeleted(r, "vehicle_pause", id, "Ruhezeit gelöscht", map[string]any{
		"vehicle_id": p.VehicleID, "from": p.From.Format(dateLayout), "until": dateOrNil(p.Until), "ended": p.Ended})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// PausePreview: GET /api/vehicles/{id}/pause-preview?from=&until= — was die Ruhezeit an
// Miete spart, aus derselben Rechnung wie die Abrechnung.
func (h *Handler) PausePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	q := r.URL.Query()
	parse := func(s string) *time.Time {
		if s == "" {
			return nil
		}
		d, err := parseOptDate(&s)
		if err != nil {
			return nil
		}
		return d
	}
	from := parse(q.Get("from"))
	if from == nil {
		t := startOfDayUTC(h.now())
		from = &t
	}
	until := parse(q.Get("until"))
	vehicles, cats, err := h.loadVehiclesWithCategories(r, 0)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	var v *models.Vehicle
	for i := range vehicles {
		if vehicles[i].ID == id {
			v = &vehicles[i]
		}
	}
	if v == nil {
		writeError(w, http.StatusNotFound, "Gefährt nicht gefunden")
		return
	}
	ags, err := h.loadAllAgreements(r.Context(), v.PersonID)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	setFlatRateCoverage(vehicles, ags, h.now())
	cat := cats[v.CategoryID]
	out := map[string]any{"flat_rate": v.FlatRateCovered, "rate": v.RateAt(cat, *from), "period": v.BillingPeriod}
	if until != nil && !until.Before(*from) && !v.FlatRateCovered {
		bare := *v
		bare.Pauses = nil
		out["days"] = int(until.Sub(*from).Hours()/24) + 1
		out["saved"] = round2(bare.CostInRange(cat, *from, until.AddDate(0, 0, 1)))
	}
	writeJSON(w, http.StatusOK, out)
}

type pauseDue struct {
	PauseID    int64      `json:"pause_id"`
	VehicleID  int64      `json:"vehicle_id"`
	Label      string     `json:"label"`
	PersonName string     `json:"person_name"`
	From       time.Time  `json:"from"`
	Until      *time.Time `json:"until"`
	Reason     string     `json:"reason"` // return | open
}

// PausesDue: GET /api/pauses/due — für die Heute-Liste: Gefährte, deren geplanter
// Rückkehrtag erreicht ist, und offene Ruhezeiten, die seit 30 Tagen laufen.
func (h *Handler) PausesDue(w http.ResponseWriter, r *http.Request) {
	today := startOfDayUTC(h.now())
	rows, err := h.Pool.Query(r.Context(),
		`SELECT vp.id, v.id, COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), c.name),
		        trim(p.first_name || ' ' || p.last_name), vp.start_date, vp.until,
		        CASE WHEN vp.until IS NOT NULL THEN 'return' ELSE 'open' END
		   FROM vehicle_pauses vp
		   JOIN vehicles v ON v.id = vp.vehicle_id
		   JOIN persons p ON p.id = v.person_id
		   JOIN categories c ON c.id = v.category_id
		  WHERE NOT vp.ended AND NOT v.archived
		    AND ((vp.until IS NOT NULL AND vp.until + 1 <= $1) OR (vp.until IS NULL AND vp.start_date <= $1 - 30))
		  ORDER BY COALESCE(vp.until + 1, vp.start_date), vp.id`, today)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []pauseDue{}
	for rows.Next() {
		var d pauseDue
		if err := rows.Scan(&d.PauseID, &d.VehicleID, &d.Label, &d.PersonName, &d.From, &d.Until, &d.Reason); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
