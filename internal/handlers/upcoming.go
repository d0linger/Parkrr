package handlers

import (
	"net/http"
	"strconv"
	"time"
)

// endingVehicle is a vehicle whose contract (end_date) is coming up.
type endingVehicle struct {
	ID         int64     `json:"id"`
	Label      string    `json:"label"`
	PersonName string    `json:"person_name"`
	EndDate    time.Time `json:"end_date"`
	DaysLeft   int       `json:"days_left"`
}

// EndingSoon lists active vehicles whose end_date falls within the next N days
// (default 30, ?days=1..365) — the dashboard's "contracts ending" reminder.
func (h *Handler) EndingSoon(w http.ResponseWriter, r *http.Request) {
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n >= 1 && n <= 365 {
			days = n
		}
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT v.id, COALESCE(NULLIF(v.label, ''), c.name),
		        trim(p.first_name || ' ' || p.last_name),
		        v.end_date, (v.end_date - CURRENT_DATE)
		   FROM vehicles v
		   JOIN persons p ON p.id = v.person_id
		   JOIN categories c ON c.id = v.category_id
		  WHERE NOT v.archived AND v.end_date IS NOT NULL
		    AND v.end_date >= CURRENT_DATE
		    AND v.end_date <= CURRENT_DATE + ($1 || ' days')::interval
		  ORDER BY v.end_date, v.id`, strconv.Itoa(days))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []endingVehicle{}
	for rows.Next() {
		var e endingVehicle
		if err := rows.Scan(&e.ID, &e.Label, &e.PersonName, &e.EndDate, &e.DaysLeft); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, e)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
