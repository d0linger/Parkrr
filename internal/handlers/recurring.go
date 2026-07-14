package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

const recurringCols = `id, person_id, description, amount, period, start_date, end_date,
	paid, paid_periods, paid_fixed, created_at, updated_at`

// scanRecurring reads one recurring_charges row (paid_fixed as raw JSON).
func scanRecurring(row pgx.Row) (models.RecurringCharge, error) {
	var rc models.RecurringCharge
	var fixedRaw []byte
	if err := row.Scan(&rc.ID, &rc.PersonID, &rc.Description, &rc.Amount, &rc.Period,
		&rc.StartDate, &rc.EndDate, &rc.Paid, &rc.PaidPeriods, &fixedRaw,
		&rc.CreatedAt, &rc.UpdatedAt); err != nil {
		return rc, err
	}
	if rc.PaidPeriods == nil {
		rc.PaidPeriods = []string{}
	}
	if len(fixedRaw) > 0 {
		_ = json.Unmarshal(fixedRaw, &rc.PaidFixed)
	}
	return rc, nil
}

// deriveRecurring fills the derived accrual/settlement fields as of now.
func deriveRecurring(rc *models.RecurringCharge, now time.Time) {
	p := rc.AsPeriod()
	rc.Accrued = round2(p.AccruedAsOf(now))
	rc.Settled = p.SettledAsOf(now)
	rc.PeriodCosts = p.ElapsedPeriodCosts(now)
}

func (h *Handler) getRecurring(ctx context.Context, id int64) (models.RecurringCharge, error) {
	return scanRecurring(h.Pool.QueryRow(ctx,
		`SELECT `+recurringCols+` FROM recurring_charges WHERE id=$1`, id))
}

func (h *Handler) loadRecurringCharges(ctx context.Context, personID int64, now time.Time) ([]models.RecurringCharge, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT `+recurringCols+` FROM recurring_charges WHERE person_id=$1 ORDER BY start_date DESC, id DESC`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.RecurringCharge{}
	for rows.Next() {
		rc, serr := scanRecurring(rows)
		if serr != nil {
			return nil, serr
		}
		deriveRecurring(&rc, now)
		out = append(out, rc)
	}
	return out, rows.Err()
}

// loadAllRecurringCharges groups every recurring charge by person (dashboard).
func (h *Handler) loadAllRecurringCharges(ctx context.Context, now time.Time) (map[int64][]models.RecurringCharge, error) {
	rows, err := h.Pool.Query(ctx, `SELECT `+recurringCols+` FROM recurring_charges`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]models.RecurringCharge{}
	for rows.Next() {
		rc, serr := scanRecurring(rows)
		if serr != nil {
			return nil, serr
		}
		deriveRecurring(&rc, now)
		out[rc.PersonID] = append(out[rc.PersonID], rc)
	}
	return out, rows.Err()
}

// recurringMonthly returns the recurring charges' accrual per calendar month of
// the given year (index 0 = January), capped at now for the current year.
func recurringMonthly(list []models.RecurringCharge, year int, now time.Time) []float64 {
	out := make([]float64, 12)
	until := now.AddDate(0, 0, 1)
	for i := range list {
		p := list[i].AsPeriod()
		for m := 0; m < 12; m++ {
			mStart := time.Date(year, time.Month(m+1), 1, 0, 0, 0, 0, time.UTC)
			mEnd := mStart.AddDate(0, 1, 0)
			if mEnd.After(until) {
				mEnd = until
			}
			if mEnd.After(mStart) {
				out[m] += p.CostInRange(mStart, mEnd)
			}
		}
	}
	return out
}

// recurringSums returns the accrued and paid totals (euros) of a person's
// recurring charges as of now, reusing the flat-rate period math.
func recurringSums(list []models.RecurringCharge, now time.Time) (accrued, paid float64) {
	until := now.AddDate(0, 0, 1)
	for i := range list {
		p := list[i].AsPeriod()
		accrued += p.CostInRange(p.StartDate, until)
		paid += float64(p.PaidCentsInRange(p.StartDate, until)) / 100
	}
	return round2(accrued), round2(paid)
}

// ListRecurringCharges returns a person's recurring charges.
func (h *Handler) ListRecurringCharges(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	list, err := h.loadRecurringCharges(r.Context(), id, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type recurringRequest struct {
	Description string   `json:"description"`
	Amount      *float64 `json:"amount"`
	Period      string   `json:"period"`
	StartDate   string   `json:"start_date"`
	EndDate     *string  `json:"end_date"`
}

// parse validates and normalizes a recurring-charge request.
func (req *recurringRequest) parse() (desc, period string, amount float64, start time.Time, end *time.Time, msg string) {
	desc = trim(req.Description)
	if desc == "" {
		return "", "", 0, time.Time{}, nil, "description is required"
	}
	if req.Amount == nil || *req.Amount < 0 {
		return "", "", 0, time.Time{}, nil, "amount must not be negative"
	}
	switch req.Period {
	case models.BillingMonthly, models.BillingYearly:
		period = req.Period
	default:
		return "", "", 0, time.Time{}, nil, "period must be monthly or yearly"
	}
	s, err := time.Parse(dateLayout, trim(req.StartDate))
	if err != nil {
		return "", "", 0, time.Time{}, nil, "start_date must be YYYY-MM-DD"
	}
	start = s
	if req.EndDate != nil && trim(*req.EndDate) != "" {
		e, eerr := time.Parse(dateLayout, trim(*req.EndDate))
		if eerr != nil {
			return "", "", 0, time.Time{}, nil, "end_date must be YYYY-MM-DD"
		}
		if !e.After(start) {
			return "", "", 0, time.Time{}, nil, "end_date must be after start_date"
		}
		end = &e
	}
	return desc, period, *req.Amount, start, end, ""
}

// CreateRecurringCharge adds a recurring extra cost for a person.
func (h *Handler) CreateRecurringCharge(w http.ResponseWriter, r *http.Request) {
	personID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req recurringRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	desc, period, amount, start, end, msg := req.parse()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var id int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO recurring_charges (person_id, description, amount, period, start_date, end_date)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		personID, desc, amount, period, start, end).Scan(&id)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "person does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create recurring charge")
		return
	}
	h.audit(r, "create", "recurring_charge", id, "added recurring cost "+desc)
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// UpdateRecurringCharge edits a recurring charge's terms (payment state kept).
func (h *Handler) UpdateRecurringCharge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req recurringRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	desc, period, amount, start, end, msg := req.parse()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE recurring_charges SET description=$1, amount=$2, period=$3, start_date=$4,
		        end_date=$5, updated_at=now() WHERE id=$6`,
		desc, amount, period, start, end, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update recurring charge")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "recurring charge not found")
		return
	}
	h.audit(r, "update", "recurring_charge", id, "updated recurring cost "+desc)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteRecurringCharge removes a recurring charge.
func (h *Handler) DeleteRecurringCharge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM recurring_charges WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete recurring charge")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "recurring charge not found")
		return
	}
	h.audit(r, "delete", "recurring_charge", id, "deleted recurring cost")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// SetRecurringChargePaid toggles the whole-charge paid flag.
func (h *Handler) SetRecurringChargePaid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Paid bool `json:"paid"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE recurring_charges SET paid=$1, updated_at=now() WHERE id=$2`, req.Paid, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update recurring charge")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "recurring charge not found")
		return
	}
	verb := "recurring marked open"
	if req.Paid {
		verb = "recurring marked paid"
	}
	h.audit(r, "update", "recurring_charge", id, verb)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// SetRecurringChargePeriodPaid settles a single elapsed sub-period: a whole
// period (amount omitted) or a fixed partial amount, mirroring the agreements.
func (h *Handler) SetRecurringChargePeriodPaid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		PeriodKey string   `json:"period_key"`
		Paid      bool     `json:"paid"`
		Amount    *float64 `json:"amount"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// A fixed partial payment must be positive; a whole-period prepayment omits it.
	if req.Paid && req.Amount != nil && *req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	rc, err := h.getRecurring(r.Context(), id)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "recurring charge not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Only periods that have begun can be paid (guards against bogus keys).
	valid := false
	p := rc.AsPeriod()
	for _, k := range p.ElapsedPeriodKeys(time.Now()) {
		if k == req.PeriodKey {
			valid = true
			break
		}
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid period")
		return
	}

	periods := removeString(rc.PaidPeriods, req.PeriodKey)
	fixed := rc.PaidFixed
	if fixed == nil {
		fixed = map[string]float64{}
	}
	delete(fixed, req.PeriodKey)
	if req.Paid {
		if req.Amount == nil {
			periods = append(periods, req.PeriodKey) // whole period prepaid
		} else {
			fixed[req.PeriodKey] = *req.Amount // fixed partial (validated > 0 above)
		}
	}
	fixedJSON, _ := json.Marshal(fixed)
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE recurring_charges SET paid_periods=$1, paid_fixed=$2, updated_at=now() WHERE id=$3`,
		periods, string(fixedJSON), id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update recurring charge")
		return
	}
	h.audit(r, "update", "recurring_charge", id, "recurring period "+req.PeriodKey)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// removeString returns s without any elements equal to v (order preserved).
func removeString(s []string, v string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
