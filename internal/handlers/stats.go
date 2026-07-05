package handlers

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// personStatsResponse is the payload for a single person's statistics.
type personStatsResponse struct {
	PersonID       int64             `json:"person_id"`
	PersonName     string            `json:"person_name"`
	TotalAccrued   float64           `json:"total_accrued"`
	TotalCharges   float64           `json:"total_charges"`
	TotalPaid      float64           `json:"total_paid"`
	Balance        float64           `json:"balance"`
	ActiveVehicles int               `json:"active_vehicles"`
	TotalVehicles  int               `json:"total_vehicles"`
	Year           int               `json:"year"`
	MonthlyAccrued []float64         `json:"monthly_accrued"`
	Years          []models.YearStat `json:"years"`
	Vehicles       []models.Vehicle  `json:"vehicles"`

	// Flat rate (Pauschale). When HasFlatRate is true, TotalAccrued/MonthlyAccrued/
	// Years come from the flat rate instead of per-vehicle costs.
	HasFlatRate     bool       `json:"has_flat_rate"`
	FlatRate        *float64   `json:"flat_rate"`
	FlatRatePeriod  string     `json:"flat_rate_period"`
	FlatRateStart   *time.Time `json:"flat_rate_start"`
	FlatRateEnd     *time.Time `json:"flat_rate_end"`
	FlatRateAccrued float64    `json:"flat_rate_accrued"`
	FlatRatePaidSum float64    `json:"flat_rate_paid_sum"` // sum of years marked paid
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

	var rentAccrued, rentPaid float64
	if person.FlatRateActive() {
		// Pauschale: one agreed amount covers all vehicles.
		resp.HasFlatRate = true
		resp.FlatRate = person.FlatRate
		resp.FlatRatePeriod = person.FlatRatePeriod
		resp.FlatRateStart = person.FlatRateStart
		resp.FlatRateEnd = person.FlatRateEnd
		rentAccrued = round2(person.FlatRateAccruedAsOf(now))
		resp.FlatRateAccrued = rentAccrued
		resp.MonthlyAccrued = flatMonthly(&person, year, now)
		years := flatYears(&person, now)
		paidYears, _ := h.loadPaidYears(r.Context(), id)
		for i := range years {
			if paidYears[years[i].Year] {
				years[i].Paid = true
				rentPaid += years[i].Cost
			}
		}
		resp.FlatRatePaidSum = round2(rentPaid)
		resp.Years = years
	} else {
		// Per-vehicle billing.
		resp.MonthlyAccrued = monthlyAccrued(vehicles, cats, year, now)
		yearly := map[int]*models.YearStat{}
		for i := range vehicles {
			v := &vehicles[i]
			cat := cats[v.CategoryID]
			rentAccrued += v.AccruedCost
			if v.Paid {
				rentPaid += v.AccruedCost
			}
			endYear := now.Year()
			if v.EndDate != nil && v.EndDate.Year() < endYear {
				endYear = v.EndDate.Year()
			}
			for y := v.StartDate.Year(); y <= endYear; y++ {
				from := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
				to := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC)
				until := now.AddDate(0, 0, 1)
				if to.After(until) {
					to = until
				}
				c := v.CostInRange(cat, from, to)
				if c <= 0 {
					continue
				}
				ys, ok := yearly[y]
				if !ok {
					ys = &models.YearStat{Year: y}
					yearly[y] = ys
				}
				ys.Cost += c
				ys.VehicleCount++
			}
		}
		for _, ys := range yearly {
			ys.Cost = round2(ys.Cost)
			resp.Years = append(resp.Years, *ys)
		}
		sort.Slice(resp.Years, func(i, j int) bool {
			return resp.Years[i].Year > resp.Years[j].Year
		})
	}

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

	// Persons billed a flat rate: their vehicles' individual costs are replaced
	// by the person's flat rate.
	flatPersons, err := h.loadFlatPersons(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	resp.TotalVehicles = len(vehicles)
	nonFlat := make([]models.Vehicle, 0, len(vehicles))
	var paidVehicles float64
	for i := range vehicles {
		v := &vehicles[i]
		cat := cats[v.CategoryID]
		if v.IsActive {
			resp.ActiveVehicles++
		}
		resp.StatusCounts[v.Status]++
		if _, flat := flatPersons[v.PersonID]; flat {
			continue // cost covered by the person's flat rate
		}
		nonFlat = append(nonFlat, *v)
		resp.AccruedTotal += v.AccruedCost
		if v.Paid {
			paidVehicles += v.AccruedCost
		}
		resp.AccruedThisYear += v.CostInRange(cat, yearStart, yearEnd)
	}

	// Add flat-rate persons' contributions (paid tracked per year).
	allPaidYears, err := h.loadAllPaidYears(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	resp.RevenueByMonth = monthlyAccrued(nonFlat, cats, resp.Year, now)
	for _, p := range flatPersons {
		resp.AccruedTotal += p.FlatRateAccruedAsOf(now)
		resp.AccruedThisYear += p.FlatRateInRange(yearStart, yearEnd)
		paid := allPaidYears[p.ID]
		for _, ys := range flatYears(p, now) {
			if paid[ys.Year] {
				paidVehicles += ys.Cost
			}
		}
		fm := flatMonthly(p, resp.Year, now)
		for m := range resp.RevenueByMonth {
			resp.RevenueByMonth[m] = round2(resp.RevenueByMonth[m] + fm[m])
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
	resp.PaidTotal = round2(paidVehicles + paidCharges)
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

// monthlyAccrued computes accrued rental cost per calendar month of a year,
// capped at today.
func monthlyAccrued(vehicles []models.Vehicle, cats map[int64]models.Category, year int, now time.Time) []float64 {
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
		var sum float64
		for i := range vehicles {
			v := &vehicles[i]
			sum += v.CostInRange(cats[v.CategoryID], from, to)
		}
		out[m] = round2(sum)
	}
	return out
}

// flatMonthly computes a person's flat-rate cost per calendar month of a year,
// capped at today and constrained to the flat-rate window.
func flatMonthly(p *models.Person, year int, now time.Time) []float64 {
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
		out[m] = round2(p.FlatRateInRange(from, to))
	}
	return out
}

// flatYears computes a person's flat-rate cost per calendar year, newest first.
func flatYears(p *models.Person, now time.Time) []models.YearStat {
	res := []models.YearStat{}
	if !p.FlatRateActive() {
		return res
	}
	endY := now.Year()
	if p.FlatRateEnd != nil && p.FlatRateEnd.Year() < endY {
		endY = p.FlatRateEnd.Year()
	}
	until := now.AddDate(0, 0, 1)
	for y := p.FlatRateStart.Year(); y <= endY; y++ {
		from := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC)
		if to.After(until) {
			to = until
		}
		c := round2(p.FlatRateInRange(from, to))
		if c <= 0 {
			continue
		}
		res = append(res, models.YearStat{Year: y, Cost: c})
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Year > res[j].Year })
	return res
}

// loadPaidYears returns the set of years marked paid for one person's flat rate.
func (h *Handler) loadPaidYears(ctx context.Context, personID int64) (map[int]bool, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT year FROM flatrate_paid_years WHERE person_id=$1`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		out[y] = true
	}
	return out, rows.Err()
}

// loadAllPaidYears returns paid flat-rate years for every person.
func (h *Handler) loadAllPaidYears(ctx context.Context) (map[int64]map[int]bool, error) {
	rows, err := h.Pool.Query(ctx, `SELECT person_id, year FROM flatrate_paid_years`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[int]bool{}
	for rows.Next() {
		var pid int64
		var y int
		if err := rows.Scan(&pid, &y); err != nil {
			return nil, err
		}
		if out[pid] == nil {
			out[pid] = map[int]bool{}
		}
		out[pid][y] = true
	}
	return out, rows.Err()
}

// loadFlatPersons returns the persons that are billed a flat rate, keyed by id.
func (h *Handler) loadFlatPersons(ctx context.Context) (map[int64]*models.Person, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT id, flat_rate, flat_rate_period, flat_rate_start, flat_rate_end, flat_rate_paid
		 FROM persons WHERE flat_rate IS NOT NULL AND flat_rate > 0 AND flat_rate_start IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*models.Person{}
	for rows.Next() {
		var p models.Person
		if err := rows.Scan(&p.ID, &p.FlatRate, &p.FlatRatePeriod,
			&p.FlatRateStart, &p.FlatRateEnd, &p.FlatRatePaid); err != nil {
			return nil, err
		}
		pp := p
		out[p.ID] = &pp
	}
	return out, rows.Err()
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
