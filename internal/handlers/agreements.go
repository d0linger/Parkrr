package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/preining/parkrr/internal/models"
)

var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// loadAgreements returns a person's flat-rate agreements with their covered
// vehicle ids and derived accrued cost (as of now), newest first.
func (h *Handler) loadAgreements(ctx context.Context, personID int64, now time.Time) ([]models.FlatRatePeriod, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT id, person_id, amount, period, start_date, end_date, paid, note, created_at, updated_at
		 FROM flat_rate_periods WHERE person_id=$1 ORDER BY start_date DESC, id DESC`, personID)
	if err != nil {
		return nil, err
	}
	out := []models.FlatRatePeriod{}
	ids := []int64{}
	for rows.Next() {
		var a models.FlatRatePeriod
		if err := rows.Scan(&a.ID, &a.PersonID, &a.Amount, &a.Period, &a.StartDate,
			&a.EndDate, &a.Paid, &a.Note, &a.CreatedAt, &a.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		a.VehicleIDs = []int64{}
		out = append(out, a)
		ids = append(ids, a.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		byID := make(map[int64]*models.FlatRatePeriod, len(out))
		for i := range out {
			byID[out[i].ID] = &out[i]
		}
		vrows, err := h.Pool.Query(ctx,
			`SELECT period_id, vehicle_id FROM flat_rate_period_vehicles WHERE period_id = ANY($1)`, ids)
		if err != nil {
			return nil, err
		}
		for vrows.Next() {
			var pid, vid int64
			if err := vrows.Scan(&pid, &vid); err != nil {
				vrows.Close()
				return nil, err
			}
			if a := byID[pid]; a != nil {
				a.VehicleIDs = append(a.VehicleIDs, vid)
			}
		}
		vrows.Close()
		if err := vrows.Err(); err != nil {
			return nil, err
		}
	}
	for i := range out {
		out[i].Accrued = round2(out[i].AccruedAsOf(now))
	}
	return out, nil
}

// ListAgreements returns the flat-rate agreements of a person.
func (h *Handler) ListAgreements(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	list, err := h.loadAgreements(r.Context(), id, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type agreementRequest struct {
	Amount     *float64 `json:"amount"`
	Period     string   `json:"period"`
	StartDate  string   `json:"start_date"`
	EndDate    *string  `json:"end_date"`
	Note       string   `json:"note"`
	VehicleIDs []int64  `json:"vehicle_ids"`
	Paid       bool     `json:"paid"`
}

// parse validates the request and returns a partially-filled agreement (without
// person id). VehicleIDs is normalised (deduplicated).
func (req *agreementRequest) parse() (models.FlatRatePeriod, string) {
	var a models.FlatRatePeriod
	if req.Amount == nil || *req.Amount <= 0 {
		return a, "amount must be greater than zero"
	}
	period := req.Period
	if period == "" {
		period = models.BillingMonthly
	}
	if period != models.BillingMonthly && period != models.BillingYearly {
		return a, "period must be 'monthly' or 'yearly'"
	}
	start, err := time.Parse(dateLayout, trim(req.StartDate))
	if err != nil {
		return a, "start_date must be YYYY-MM-DD"
	}
	end, err := parseOptDate(req.EndDate)
	if err != nil {
		return a, "end_date must be YYYY-MM-DD"
	}
	if end != nil && end.Before(start) {
		return a, "end_date must not be before start_date"
	}
	seen := map[int64]bool{}
	vids := []int64{}
	for _, v := range req.VehicleIDs {
		if v > 0 && !seen[v] {
			seen[v] = true
			vids = append(vids, v)
		}
	}
	a.Amount, a.Period, a.StartDate, a.EndDate = *req.Amount, period, start, end
	a.Note, a.VehicleIDs, a.Paid = trim(req.Note), vids, req.Paid
	return a, ""
}

// agreementsConflict reports whether a and b overlap in time AND share at least
// one covered vehicle (empty VehicleIDs = all vehicles).
func agreementsConflict(a, b models.FlatRatePeriod) bool {
	aEnd, bEnd := farFuture, farFuture
	if a.EndDate != nil {
		aEnd = *a.EndDate
	}
	if b.EndDate != nil {
		bEnd = *b.EndDate
	}
	if !a.StartDate.Before(bEnd) || !b.StartDate.Before(aEnd) {
		return false // disjoint in time
	}
	if len(a.VehicleIDs) == 0 || len(b.VehicleIDs) == 0 {
		return true // one covers all vehicles
	}
	set := make(map[int64]bool, len(a.VehicleIDs))
	for _, v := range a.VehicleIDs {
		set[v] = true
	}
	for _, v := range b.VehicleIDs {
		if set[v] {
			return true
		}
	}
	return false
}

// checkOverlap returns an error message if the candidate conflicts with an
// existing agreement of the person (excludeID skips the row being updated).
func (h *Handler) checkOverlap(ctx context.Context, personID, excludeID int64, cand models.FlatRatePeriod, now time.Time) (string, error) {
	existing, err := h.loadAgreements(ctx, personID, now)
	if err != nil {
		return "", err
	}
	for i := range existing {
		if existing[i].ID == excludeID {
			continue
		}
		if agreementsConflict(cand, existing[i]) {
			return "overlaps an existing agreement for one or more of the same vehicles", nil
		}
	}
	return "", nil
}

// vehiclesBelongTo reports whether every id in vids is a vehicle of personID.
func (h *Handler) vehiclesBelongTo(ctx context.Context, personID int64, vids []int64) (bool, error) {
	if len(vids) == 0 {
		return true, nil
	}
	var n int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM vehicles WHERE id = ANY($1) AND person_id = $2`, vids, personID).Scan(&n); err != nil {
		return false, err
	}
	return n == len(vids), nil
}

// validateAgreement checks vehicle ownership and time/vehicle overlap. Returns a
// (clientMessage, httpStatus) to send, or ("", 0) when the candidate is valid.
func (h *Handler) validateAgreement(ctx context.Context, personID, excludeID int64, cand models.FlatRatePeriod) (string, int) {
	ok, err := h.vehiclesBelongTo(ctx, personID, cand.VehicleIDs)
	if err != nil {
		return "query failed", http.StatusInternalServerError
	}
	if !ok {
		return "all covered vehicles must belong to this person", http.StatusBadRequest
	}
	msg, err := h.checkOverlap(ctx, personID, excludeID, cand, time.Now())
	if err != nil {
		return "query failed", http.StatusInternalServerError
	}
	if msg != "" {
		return msg, http.StatusConflict
	}
	return "", 0
}

// CreateAgreement adds a flat-rate agreement to a person.
func (h *Handler) CreateAgreement(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req agreementRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cand, msg := req.parse()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if m, code := h.validateAgreement(r.Context(), pid, 0, cand); m != "" {
		writeError(w, code, m)
		return
	}
	if err := h.saveAgreement(r.Context(), 0, pid, cand); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create agreement")
		return
	}
	h.audit(r, "create", "flatrate", pid, "Pauschale angelegt für "+h.personLabel(r, pid))
	h.writeAgreements(w, r, pid)
}

// UpdateAgreement edits an existing agreement.
func (h *Handler) UpdateAgreement(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var pid int64
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT person_id FROM flat_rate_periods WHERE id=$1`, id).Scan(&pid); err != nil {
		writeError(w, http.StatusNotFound, "agreement not found")
		return
	}
	var req agreementRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cand, msg := req.parse()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if m, code := h.validateAgreement(r.Context(), pid, id, cand); m != "" {
		writeError(w, code, m)
		return
	}
	if err := h.saveAgreement(r.Context(), id, pid, cand); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update agreement")
		return
	}
	h.audit(r, "update", "flatrate", id, "Pauschale geändert für "+h.personLabel(r, pid))
	h.writeAgreements(w, r, pid)
}

// saveAgreement inserts (id==0) or updates an agreement and replaces its covered
// vehicles, in one transaction.
func (h *Handler) saveAgreement(ctx context.Context, id, personID int64, a models.FlatRatePeriod) error {
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if id == 0 {
		if err := tx.QueryRow(ctx,
			`INSERT INTO flat_rate_periods (person_id, amount, period, start_date, end_date, paid, note)
			 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			personID, a.Amount, a.Period, a.StartDate, a.EndDate, a.Paid, a.Note).Scan(&id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx,
			`UPDATE flat_rate_periods SET amount=$1, period=$2, start_date=$3, end_date=$4,
			        paid=$5, note=$6, updated_at=now() WHERE id=$7`,
			a.Amount, a.Period, a.StartDate, a.EndDate, a.Paid, a.Note, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM flat_rate_period_vehicles WHERE period_id=$1`, id); err != nil {
			return err
		}
	}
	for _, vid := range a.VehicleIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO flat_rate_period_vehicles (period_id, vehicle_id) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, id, vid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// DeleteAgreement removes an agreement.
func (h *Handler) DeleteAgreement(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var pid int64
	_ = h.Pool.QueryRow(r.Context(), `SELECT person_id FROM flat_rate_periods WHERE id=$1`, id).Scan(&pid)
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM flat_rate_periods WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete agreement")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "agreement not found")
		return
	}
	h.audit(r, "delete", "flatrate", id, "Pauschale gelöscht für "+h.personLabel(r, pid))
	h.writeAgreements(w, r, pid)
}

type agreementPaidRequest struct {
	Paid bool `json:"paid"`
}

// SetAgreementPaid toggles an agreement's paid status.
func (h *Handler) SetAgreementPaid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req agreementPaidRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var pid int64
	if err := h.Pool.QueryRow(r.Context(),
		`UPDATE flat_rate_periods SET paid=$1, updated_at=now() WHERE id=$2 RETURNING person_id`,
		req.Paid, id).Scan(&pid); err != nil {
		writeError(w, http.StatusNotFound, "agreement not found")
		return
	}
	state := "offen"
	if req.Paid {
		state = "bezahlt"
	}
	h.audit(r, "update", "flatrate", id, "Pauschale "+h.personLabel(r, pid)+" "+state)
	h.writeAgreements(w, r, pid)
}

func (h *Handler) writeAgreements(w http.ResponseWriter, r *http.Request, personID int64) {
	list, err := h.loadAgreements(r.Context(), personID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// loadAllAgreements returns every agreement, grouped by person id.
func (h *Handler) loadAllAgreements(ctx context.Context) (map[int64][]models.FlatRatePeriod, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT id, person_id, amount, period, start_date, end_date, paid FROM flat_rate_periods`)
	if err != nil {
		return nil, err
	}
	out := map[int64][]models.FlatRatePeriod{}
	byID := map[int64]*models.FlatRatePeriod{}
	for rows.Next() {
		var a models.FlatRatePeriod
		if err := rows.Scan(&a.ID, &a.PersonID, &a.Amount, &a.Period, &a.StartDate, &a.EndDate, &a.Paid); err != nil {
			rows.Close()
			return nil, err
		}
		a.VehicleIDs = []int64{}
		out[a.PersonID] = append(out[a.PersonID], a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for pid := range out {
		for i := range out[pid] {
			byID[out[pid][i].ID] = &out[pid][i]
		}
	}
	vrows, err := h.Pool.Query(ctx, `SELECT period_id, vehicle_id FROM flat_rate_period_vehicles`)
	if err != nil {
		return nil, err
	}
	for vrows.Next() {
		var pid, vid int64
		if err := vrows.Scan(&pid, &vid); err != nil {
			vrows.Close()
			return nil, err
		}
		if a := byID[pid]; a != nil {
			a.VehicleIDs = append(a.VehicleIDs, vid)
		}
	}
	vrows.Close()
	return out, vrows.Err()
}

// coveringAgreements returns the agreements that cover the given vehicle.
func coveringAgreements(agreements []models.FlatRatePeriod, vehicleID int64) []models.FlatRatePeriod {
	var out []models.FlatRatePeriod
	for i := range agreements {
		if agreements[i].Covers(vehicleID) {
			out = append(out, agreements[i])
		}
	}
	return out
}

// personRent computes rent accrued and paid over [from, to) for a person from
// their flat-rate agreements plus per-vehicle costs for time NOT covered by any
// agreement. Covered vehicles contribute only their uncovered portion.
func personRent(agreements []models.FlatRatePeriod, vehicles []models.Vehicle, cats map[int64]models.Category, from, to time.Time) (accrued, paid float64) {
	for i := range agreements {
		c := agreements[i].CostInRange(from, to)
		accrued += c
		if agreements[i].Paid {
			paid += c
		}
	}
	for i := range vehicles {
		v := &vehicles[i]
		u := models.VehicleUncoveredCost(v, cats[v.CategoryID], from, to, coveringAgreements(agreements, v.ID))
		accrued += u
		if v.Paid {
			paid += u
		}
	}
	return accrued, paid
}
