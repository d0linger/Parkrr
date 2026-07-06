package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/preining/parkrr/internal/models"
)

type flatRateRequest struct {
	Enabled   bool     `json:"enabled"`
	Amount    *float64 `json:"amount"`
	Period    string   `json:"period"`
	StartDate string   `json:"start_date"`
	EndDate   *string  `json:"end_date"`
}

// SetFlatRate sets or clears a person's flat rate (Pauschale). When cleared the
// person falls back to per-vehicle billing.
func (h *Handler) SetFlatRate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req flatRateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !req.Enabled || req.Amount == nil || *req.Amount <= 0 {
		ct, err := h.Pool.Exec(r.Context(),
			`UPDATE persons SET flat_rate=NULL, flat_rate_start=NULL, flat_rate_end=NULL,
			        flat_rate_paid=FALSE, updated_at=now() WHERE id=$1`, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not update person")
			return
		}
		if ct.RowsAffected() == 0 {
			writeError(w, http.StatusNotFound, "person not found")
			return
		}
		// Drop per-year paid marks so a later re-enrolment starts clean instead
		// of resurfacing stale "bezahlt" years from the old arrangement.
		_, _ = h.Pool.Exec(r.Context(), `DELETE FROM flatrate_paid_years WHERE person_id=$1`, id)
		h.audit(r, "update", "person", id, "removed flat rate for "+h.personLabel(r, id))
		h.writePerson(w, r, id)
		return
	}

	period := req.Period
	if period == "" {
		period = models.BillingMonthly
	}
	if period != models.BillingMonthly && period != models.BillingYearly {
		writeError(w, http.StatusBadRequest, "period must be 'monthly' or 'yearly'")
		return
	}
	start, err := time.Parse(dateLayout, trim(req.StartDate))
	if err != nil {
		writeError(w, http.StatusBadRequest, "start_date must be YYYY-MM-DD")
		return
	}
	endPtr, err := parseOptDate(req.EndDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "end_date must be YYYY-MM-DD")
		return
	}
	if endPtr != nil && endPtr.Before(start) {
		writeError(w, http.StatusBadRequest, "end_date must not be before start_date")
		return
	}

	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE persons SET flat_rate=$1, flat_rate_period=$2, flat_rate_start=$3,
		        flat_rate_end=$4, updated_at=now() WHERE id=$5`,
		*req.Amount, period, start, endPtr, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update person")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	h.audit(r, "update", "person", id, "set flat rate for "+h.personLabel(r, id))
	h.writePerson(w, r, id)
}

type flatRatePaidRequest struct {
	Year int  `json:"year"`
	Paid bool `json:"paid"`
}

// SetFlatRatePaid toggles the paid state of a person's flat rate for one year.
func (h *Handler) SetFlatRatePaid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req flatRatePaidRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Year < 2000 || req.Year > 2100 {
		writeError(w, http.StatusBadRequest, "invalid year")
		return
	}

	var err error
	if req.Paid {
		_, err = h.Pool.Exec(r.Context(),
			`INSERT INTO flatrate_paid_years (person_id, year) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, id, req.Year)
	} else {
		_, err = h.Pool.Exec(r.Context(),
			`DELETE FROM flatrate_paid_years WHERE person_id=$1 AND year=$2`, id, req.Year)
	}
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusNotFound, "person not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update flat rate")
		return
	}
	state := "open"
	if req.Paid {
		state = "paid"
	}
	h.audit(r, "update", "person", id,
		"Pauschale "+h.personLabel(r, id)+" "+strconv.Itoa(req.Year)+" marked "+state)
	h.writePerson(w, r, id)
}

// personLabel returns a person's display name for audit messages ("" on error).
func (h *Handler) personLabel(r *http.Request, id int64) string {
	var name string
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT trim(first_name || ' ' || last_name) FROM persons WHERE id=$1`, id).Scan(&name)
	return name
}

func (h *Handler) writePerson(w http.ResponseWriter, r *http.Request, id int64) {
	p, err := h.getPerson(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load person")
		return
	}
	writeJSON(w, http.StatusOK, p)
}
