package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// personStatsResponse is the payload for a single person's statistics.
type personStatsResponse struct {
	PersonID       int64                   `json:"person_id"`
	PersonName     string                  `json:"person_name"`
	TotalAccrued   float64                 `json:"total_accrued"`
	TotalCharges   float64                 `json:"total_charges"`
	TotalPaid      float64                 `json:"total_paid"`
	Balance        float64                 `json:"balance"`
	ActiveVehicles int                     `json:"active_vehicles"`
	TotalVehicles  int                     `json:"total_vehicles"`
	Year           int                     `json:"year"`
	MonthlyAccrued []float64               `json:"monthly_accrued"`
	Years          []models.YearStat       `json:"years"`
	Vehicles       []models.Vehicle        `json:"vehicles"`
	Agreements     []models.FlatRatePeriod `json:"agreements"`
	HasFlatRate    bool                    `json:"has_flat_rate"`
}

// PersonStats returns per-year and per-month cost statistics plus the open
// balance for a single person. Accepts ?year= for the monthly breakdown.
func (h *Handler) PersonStats(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	person, err := h.getPerson(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}

	vehicles, cats, err := h.loadVehiclesWithCategories(r, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	now := time.Now()
	year := now.Year()
	if y := r.URL.Query().Get("year"); y != "" {
		if n, err := strconv.Atoi(y); err == nil && n >= 2000 && n <= 2100 {
			year = n
		}
	}

	resp := personStatsResponse{
		PersonID:      id,
		PersonName:    trim(person.FirstName + " " + person.LastName),
		TotalVehicles: len(vehicles),
		Year:          year,
		Years:         []models.YearStat{},
		Vehicles:      vehicles,
	}
	for i := range vehicles {
		if vehicles[i].IsActive {
			resp.ActiveVehicles++
		}
	}

	agreements, err := h.loadAgreements(r.Context(), id, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	resp.Agreements = agreements
	resp.HasFlatRate = len(agreements) > 0

	// Rent = flat-rate agreements + per-vehicle cost for time not covered by an
	// agreement. Covered vehicles contribute only their uncovered portion.
	until := now.AddDate(0, 0, 1)
	rentAccrued, rentPaid := personRent(agreements, vehicles, cats, time.Time{}, until)
	resp.MonthlyAccrued = personMonthly(agreements, vehicles, cats, year, now)
	resp.Years = personYears(agreements, vehicles, cats, now)

	// Extra charges are billed on top of rent; a charge counts as paid when its
	// vehicle is marked paid (flat-rate persons: rent-paid via the flat slider).
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT COALESCE(sum(amount * quantity), 0) FROM charges WHERE person_id=$1`, id,
	).Scan(&resp.TotalCharges)
	var paidCharges float64
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT COALESCE(sum(c.amount * c.quantity), 0)
		 FROM charges c JOIN vehicles v ON v.id = c.vehicle_id
		 WHERE c.person_id=$1 AND v.paid`, id,
	).Scan(&paidCharges)

	resp.TotalAccrued = round2(rentAccrued)
	resp.TotalCharges = round2(resp.TotalCharges)
	resp.TotalPaid = round2(rentPaid + paidCharges)
	resp.Balance = round2(resp.TotalAccrued + resp.TotalCharges - resp.TotalPaid)

	writeJSON(w, http.StatusOK, resp)
}

// overviewResponse summarizes the whole dataset for the dashboard.
type overviewResponse struct {
	TotalPersons     int            `json:"total_persons"`
	TotalVehicles    int            `json:"total_vehicles"`
	ActiveVehicles   int            `json:"active_vehicles"`
	TotalCategories  int            `json:"total_categories"`
	AccruedThisYear  float64        `json:"accrued_this_year"`
	AccruedTotal     float64        `json:"accrued_total"`
	PaidTotal        float64        `json:"paid_total"`
	OutstandingTotal float64        `json:"outstanding_total"`
	Year             int            `json:"year"`
	StatusCounts     map[string]int `json:"status_counts"`
	RevenueByMonth   []float64      `json:"revenue_by_month"`
	ChargesByMonth   []float64      `json:"charges_by_month"`
}

// Overview returns aggregate statistics for the dashboard. Accepts ?year=.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := overviewResponse{
		StatusCounts: map[string]int{
			models.StatusReserved: 0, models.StatusStored: 0,
			models.StatusCollected: 0, models.StatusCancelled: 0,
		},
	}

	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM persons`).Scan(&resp.TotalPersons); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM categories`).Scan(&resp.TotalCategories); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	vehicles, cats, err := h.loadVehiclesWithCategories(r, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	now := time.Now()
	resp.Year = now.Year()
	if y := r.URL.Query().Get("year"); y != "" {
		if n, err := strconv.Atoi(y); err == nil && n >= 2000 && n <= 2100 {
			resp.Year = n
		}
	}
	yearStart := time.Date(resp.Year, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := time.Date(resp.Year+1, 1, 1, 0, 0, 0, 0, time.UTC)
	until := now.AddDate(0, 0, 1)
	if yearEnd.After(until) {
		yearEnd = until
	}

	resp.TotalVehicles = len(vehicles)
	vehByPerson := map[int64][]models.Vehicle{}
	for i := range vehicles {
		v := &vehicles[i]
		if v.IsActive {
			resp.ActiveVehicles++
		}
		resp.StatusCounts[v.Status]++
		vehByPerson[v.PersonID] = append(vehByPerson[v.PersonID], *v)
	}

	// Rent per person = flat-rate agreements + per-vehicle cost for uncovered
	// time. Aggregate across every person that has vehicles or agreements.
	agByPerson, err := h.loadAllAgreements(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	personIDs := map[int64]struct{}{}
	for pid := range vehByPerson {
		personIDs[pid] = struct{}{}
	}
	for pid := range agByPerson {
		personIDs[pid] = struct{}{}
	}
	resp.RevenueByMonth = make([]float64, 12)
	var paidRent float64
	for pid := range personIDs {
		ag, vs := agByPerson[pid], vehByPerson[pid]
		tAcc, tPaid := personRent(ag, vs, cats, time.Time{}, until)
		resp.AccruedTotal += tAcc
		paidRent += tPaid
		yAcc, _ := personRent(ag, vs, cats, yearStart, yearEnd)
		resp.AccruedThisYear += yAcc
		pm := personMonthly(ag, vs, cats, resp.Year, now)
		for m := range resp.RevenueByMonth {
			resp.RevenueByMonth[m] = round2(resp.RevenueByMonth[m] + pm[m])
		}
	}
	resp.AccruedTotal = round2(resp.AccruedTotal)
	resp.AccruedThisYear = round2(resp.AccruedThisYear)

	resp.ChargesByMonth = h.sumByMonth(ctx, resp.Year,
		`SELECT extract(month from charged_on)::int, COALESCE(sum(amount*quantity),0)
		 FROM charges WHERE extract(year from charged_on)=$1 GROUP BY 1`)

	// Paid / outstanding are derived from the per-vehicle paid slider.
	var totalCharges, paidCharges float64
	_ = h.Pool.QueryRow(ctx, `SELECT COALESCE(sum(amount*quantity),0) FROM charges`).Scan(&totalCharges)
	_ = h.Pool.QueryRow(ctx,
		`SELECT COALESCE(sum(c.amount*c.quantity),0)
		 FROM charges c JOIN vehicles v ON v.id = c.vehicle_id WHERE v.paid`).Scan(&paidCharges)
	resp.PaidTotal = round2(paidRent + paidCharges)
	resp.OutstandingTotal = round2(resp.AccruedTotal + totalCharges - resp.PaidTotal)

	writeJSON(w, http.StatusOK, resp)
}

// sumByMonth runs a "month -> sum" query for a year and returns a 12-slot slice
// (index 0 = January). Errors yield an all-zero slice.
func (h *Handler) sumByMonth(ctx context.Context, year int, query string) []float64 {
	out := make([]float64, 12)
	rows, err := h.Pool.Query(ctx, query, year)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var month int
		var sum float64
		if err := rows.Scan(&month, &sum); err != nil {
			return out
		}
		if month >= 1 && month <= 12 {
			out[month-1] = round2(sum)
		}
	}
	return out
}

// personMonthly computes combined rent (flat-rate agreements + uncovered
// per-vehicle cost) per calendar month of a year, capped at today.
func personMonthly(agreements []models.FlatRatePeriod, vehicles []models.Vehicle, cats map[int64]models.Category, year int, now time.Time) []float64 {
	out := make([]float64, 12)
	until := now.AddDate(0, 0, 1)
	for m := 0; m < 12; m++ {
		from := time.Date(year, time.Month(m+1), 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 1, 0)
		if to.After(until) {
			to = until
		}
		if !to.After(from) {
			continue
		}
		acc, _ := personRent(agreements, vehicles, cats, from, to)
		out[m] = round2(acc)
	}
	return out
}

// personYears computes combined rent per calendar year, newest first, from the
// earliest agreement/vehicle start through today.
func personYears(agreements []models.FlatRatePeriod, vehicles []models.Vehicle, cats map[int64]models.Category, now time.Time) []models.YearStat {
	res := []models.YearStat{}
	until := now.AddDate(0, 0, 1)
	startYear := now.Year()
	for i := range vehicles {
		if y := vehicles[i].StartDate.Year(); y < startYear {
			startYear = y
		}
	}
	for i := range agreements {
		if y := agreements[i].StartDate.Year(); y < startYear {
			startYear = y
		}
	}
	for y := now.Year(); y >= startYear; y-- {
		from := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC)
		if to.After(until) {
			to = until
		}
		if !to.After(from) {
			continue
		}
		acc, _ := personRent(agreements, vehicles, cats, from, to)
		if c := round2(acc); c > 0 {
			res = append(res, models.YearStat{Year: y, Cost: c})
		}
	}
	return res
}

// loadVehiclesWithCategories loads vehicles (optionally for one person, when
// personID > 0) enriched with derived cost fields, plus a category lookup map.
func (h *Handler) loadVehiclesWithCategories(r *http.Request, personID int64) ([]models.Vehicle, map[int64]models.Category, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if personID > 0 {
		rows, err = h.Pool.Query(r.Context(), vehicleSelect+` WHERE v.person_id = $1 ORDER BY v.start_date`, personID)
	} else {
		rows, err = h.Pool.Query(r.Context(), vehicleSelect+` ORDER BY v.start_date`)
	}
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	now := time.Now()
	vehicles := []models.Vehicle{}
	cats := map[int64]models.Category{}
	for rows.Next() {
		v, cat, err := scanVehicleRow(rows)
		if err != nil {
			return nil, nil, err
		}
		enrich(&v, cat, now)
		cats[cat.ID] = cat
		vehicles = append(vehicles, v)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return vehicles, cats, nil
}
