package handlers

import (
	"net/http"
	"time"
)

// occupancyEntry ist ein Belegungszeitraum (Hundert 79).
type occupancyEntry struct {
	SpotID       int64      `json:"spot_id"`
	SpotLabel    string     `json:"spot_label"`
	VehicleID    int64      `json:"vehicle_id"`
	VehicleLabel string     `json:"vehicle_label"`
	PersonID     *int64     `json:"person_id,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"` // nil = steht noch dort
}

// SpotHistory beantwortet "wer stand auf diesem Platz, und wann?" — gespeist vom
// Belegungs-Trigger (Migration 061), der JEDEN Weg einer Umplatzierung sieht.
func (h *Handler) SpotHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	h.occupancyRows(w, r, `WHERE spot_id=$1`, id)
}

// VehicleSpotHistory ist die Gegenrichtung: wo stand dieses Gefährt?
func (h *Handler) VehicleSpotHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	h.occupancyRows(w, r, `WHERE vehicle_id=$1`, id)
}

func (h *Handler) occupancyRows(w http.ResponseWriter, r *http.Request, where string, arg int64) {
	limit, offset := pageParams(r, 100, 500)
	rows, err := h.Pool.Query(r.Context(),
		`SELECT spot_id, spot_label, vehicle_id, vehicle_label, person_id, started_at, ended_at
		   FROM spot_occupancy_history `+where+`
		  ORDER BY started_at DESC, id DESC LIMIT $2 OFFSET $3`, arg, limit, offset)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []occupancyEntry{}
	for rows.Next() {
		var e occupancyEntry
		if err := rows.Scan(&e.SpotID, &e.SpotLabel, &e.VehicleID, &e.VehicleLabel,
			&e.PersonID, &e.StartedAt, &e.EndedAt); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
