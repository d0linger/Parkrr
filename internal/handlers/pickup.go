package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5"
)

// Abholung in Zeitfenstern (Ausbau 23). Der Betreiber legt wöchentliche Fenster fest
// (z. B. Samstag 9:00–12:00 in 30-Minuten-Schritten, höchstens zwei je Zeitpunkt).
// Das Portal bietet dann nur freie Zeitpunkte an; ohne Fenster bleibt es beim
// Wunschdatum wie bisher.

type pickupWindow struct {
	ID       int64 `json:"id,omitempty"`
	Weekday  int   `json:"weekday"` // ISO 1 = Montag … 7 = Sonntag
	StartMin int   `json:"start_min"`
	EndMin   int   `json:"end_min"`
	SlotMin  int   `json:"slot_min"`
	Capacity int   `json:"capacity"`
}

func (p pickupWindow) valid() bool {
	return p.Weekday >= 1 && p.Weekday <= 7 && p.StartMin >= 0 && p.EndMin <= 1440 && p.EndMin > p.StartMin &&
		p.SlotMin >= 10 && p.SlotMin <= 240 && p.Capacity >= 1 && p.Capacity <= 20
}

func (h *Handler) loadPickupWindows(ctx context.Context, q dbQuerier) ([]pickupWindow, error) {
	rows, err := q.Query(ctx, `SELECT id, weekday, start_min, end_min, slot_min, capacity FROM pickup_windows ORDER BY weekday, start_min, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []pickupWindow{}
	for rows.Next() {
		var p pickupWindow
		if err := rows.Scan(&p.ID, &p.Weekday, &p.StartMin, &p.EndMin, &p.SlotMin, &p.Capacity); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPickupWindows: GET /api/pickup-windows.
func (h *Handler) GetPickupWindows(w http.ResponseWriter, r *http.Request) {
	list, err := h.loadPickupWindows(r.Context(), h.Pool)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// SavePickupWindows: PUT /api/pickup-windows — ersetzt die ganze Liste.
func (h *Handler) SavePickupWindows(w http.ResponseWriter, r *http.Request) {
	var in []pickupWindow
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(in) > 50 {
		writeError(w, http.StatusBadRequest, "höchstens 50 Zeitfenster")
		return
	}
	for _, p := range in {
		if !p.valid() {
			writeError(w, http.StatusBadRequest, "Zeitfenster ungültig (Wochentag 1–7, Ende nach Beginn, Schritt 10–240 min, 1–20 je Zeitpunkt)")
			return
		}
	}
	old, err := h.loadPickupWindows(r.Context(), h.Pool)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	err = pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `DELETE FROM pickup_windows`); err != nil {
			return err
		}
		for _, p := range in {
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO pickup_windows (weekday, start_min, end_min, slot_min, capacity) VALUES ($1,$2,$3,$4,$5)`,
				p.Weekday, p.StartMin, p.EndMin, p.SlotMin, p.Capacity); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	h.auditChange(r, "update", "pickup_windows", 0, fmt.Sprintf("Abholzeitfenster gespeichert (%d)", len(in)),
		diffFields(map[string]any{"windows": len(old)}, map[string]any{"windows": len(in)}))
	list, _ := h.loadPickupWindows(r.Context(), h.Pool)
	writeJSON(w, http.StatusOK, list)
}

type pickupSlot struct {
	Date string `json:"date"` // YYYY-MM-DD
	Time string `json:"time"` // HH:MM
	Free int    `json:"free"`
}

func minutesToClock(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// pickupSlots liefert die freien Zeitpunkte von heute (ab der nächsten vollen
// Stunde) bis days Tage voraus. Belegt zählt jeder offene oder angenommene
// Abholwunsch mit genau diesem Datum und dieser Uhrzeit.
func (h *Handler) pickupSlots(ctx context.Context, q dbQuerier, days int) ([]pickupSlot, error) {
	wins, err := h.loadPickupWindows(ctx, q)
	if err != nil || len(wins) == 0 {
		return []pickupSlot{}, err
	}
	now := h.now()
	today := startOfDayUTC(now)
	last := today.AddDate(0, 0, days)
	taken := map[string]int{}
	rows, err := q.Query(ctx,
		`SELECT payload->>'date', payload->>'time' FROM portal_requests
		  WHERE kind = 'pickup' AND status IN ('offen', 'erledigt')
		    AND payload ? 'time' AND (payload->>'date') BETWEEN $1 AND $2`,
		today.Format(dateLayout), last.Format(dateLayout))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d, t string
		if err := rows.Scan(&d, &t); err != nil {
			rows.Close()
			return nil, err
		}
		taken[d+" "+t]++
	}
	rows.Close()
	nowMin := now.Hour()*60 + now.Minute()
	out := []pickupSlot{}
	for d := today; !d.After(last); d = d.AddDate(0, 0, 1) {
		iso := (int(d.Weekday())+6)%7 + 1
		for _, w := range wins {
			if w.Weekday != iso {
				continue
			}
			for m := w.StartMin; m+w.SlotMin <= w.EndMin; m += w.SlotMin {
				if d.Equal(today) && m <= nowMin+60 {
					continue // heute nur mit mindestens einer Stunde Vorlauf
				}
				key := d.Format(dateLayout) + " " + minutesToClock(m)
				if free := w.Capacity - taken[key]; free > 0 {
					out = append(out, pickupSlot{Date: d.Format(dateLayout), Time: minutesToClock(m), Free: free})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].Time < out[j].Time
	})
	return out, nil
}

// PortalPickupSlots: GET /api/portal/pickup-slots (Portal-Token) — freie Zeitpunkte
// der nächsten 28 Tage; leer, wenn der Betreiber keine Fenster festgelegt hat.
func (h *Handler) PortalPickupSlots(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.portalPerson(r); !ok {
		writeError(w, http.StatusNotFound, "Link ungültig oder abgelaufen")
		return
	}
	slots, err := h.pickupSlots(r.Context(), h.Pool, 28)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, slots)
}

// slotStillFree prüft in der Einreichungstransaktion, ob der gewählte Zeitpunkt in
// einem Fenster liegt und noch Platz hat.
func (h *Handler) slotStillFree(ctx context.Context, tx pgx.Tx, date, clock string) (bool, error) {
	slots, err := h.pickupSlots(ctx, tx, 60)
	if err != nil {
		return false, err
	}
	for _, s := range slots {
		if s.Date == date && s.Time == clock {
			return true, nil
		}
	}
	return false, nil
}

// pickupWindowsActive meldet, ob Zeitfenster festgelegt sind (dann ist eine Uhrzeit Pflicht).
func (h *Handler) pickupWindowsActive(ctx context.Context, q dbQuerier) (bool, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM pickup_windows`).Scan(&n)
	return n > 0, err
}
