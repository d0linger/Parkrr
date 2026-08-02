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
	PersonID         int64                    `json:"person_id"`
	PersonName       string                   `json:"person_name"`
	TotalAccrued     float64                  `json:"total_accrued"`
	TotalCharges     float64                  `json:"total_charges"`
	TotalPaid        float64                  `json:"total_paid"`
	Balance          float64                  `json:"balance"`
	PaymentsTotal    float64                  `json:"payments_total"` // recorded money-in (all time)
	PaymentsYear     float64                  `json:"payments_year"`  // recorded money-in in the selected year
	Credit           float64                  `json:"credit"`         // Guthaben: payments not (yet) allocated to items
	ActiveVehicles   int                      `json:"active_vehicles"`
	TotalVehicles    int                      `json:"total_vehicles"`
	Year             int                      `json:"year"`
	MonthlyAccrued   []float64                `json:"monthly_accrued"`
	Years            []models.YearStat        `json:"years"`
	Vehicles         []models.Vehicle         `json:"vehicles"`
	Agreements       []models.FlatRatePeriod  `json:"agreements"`
	RecurringCharges []models.RecurringCharge `json:"recurring_charges"`
	HasFlatRate      bool                     `json:"has_flat_rate"`
}

// parseYearParam reads an optional ?year= query parameter, returning def when it
// is absent or outside the supported 2000–2100 range.
func parseYearParam(r *http.Request, def int) int {
	if y := r.URL.Query().Get("year"); y != "" {
		if n, err := strconv.Atoi(y); err == nil && n >= 2000 && n <= 2100 {
			return n
		}
	}
	return def
}

// chargeSettled reports whether an extra charge counts as paid. A standalone
// charge (no vehicle) uses its own flag. A vehicle-bound charge is settled when a
// covering Pauschale's sub-period for the charge's date is paid in full — so
// marking that Pauschale paid also settles the bound extras — and otherwise falls
// back to the vehicle's own paid flag.
func chargeSettled(agreements []models.FlatRatePeriod, vehicleID *int64, chargedOn time.Time, ownPaid, vehiclePaid bool) bool {
	if vehicleID == nil {
		return ownPaid
	}
	for i := range agreements {
		a := &agreements[i]
		if a.Covers(*vehicleID) && a.ActiveAt(chargedOn) && a.PeriodPaidAt(chargedOn) {
			return true
		}
	}
	return vehiclePaid
}

// chargeAmounts computes one charge row's total (amount × quantity) and its paid
// portion — the whole total when chargeSettled reports it paid, otherwise 0. The
// vehicle's stored paid flag is looked up in vehPaid.
func chargeAmounts(agreements []models.FlatRatePeriod, vehPaid map[int64]bool, vid *int64, amount, qty float64, chargedOn time.Time, ownPaid bool) (total, paid float64) {
	total = amount * qty
	vp := false
	if vid != nil {
		vp = vehPaid[*vid]
	}
	if chargeSettled(agreements, vid, chargedOn, ownPaid, vp) {
		paid = total
	}
	return total, paid
}

// personChargeSums returns a person's total charges and the paid portion, with a
// vehicle-bound charge settled via its covering Pauschale (or the vehicle's own
// paid flag). vehPaid maps a vehicle id to its stored paid flag.
func (h *Handler) personChargeSums(ctx context.Context, personID int64, agreements []models.FlatRatePeriod, vehPaid map[int64]bool) (total, paid float64, err error) {
	rows, qerr := h.Pool.Query(ctx,
		`SELECT vehicle_id, amount, quantity, charged_on, paid FROM charges WHERE person_id=$1`, personID)
	if qerr != nil {
		return 0, 0, qerr
	}
	defer rows.Close()
	for rows.Next() {
		var vid *int64
		var amount, qty float64
		var chargedOn time.Time
		var ownPaid bool
		if serr := rows.Scan(&vid, &amount, &qty, &chargedOn, &ownPaid); serr != nil {
			return 0, 0, serr
		}
		t, p := chargeAmounts(agreements, vehPaid, vid, amount, qty, chargedOn, ownPaid)
		total += t
		paid += p
	}
	return total, paid, rows.Err()
}

// vehiclePaidMap indexes vehicles by id -> stored paid flag.
func vehiclePaidMap(vehicles []models.Vehicle) map[int64]bool {
	m := make(map[int64]bool, len(vehicles))
	for i := range vehicles {
		m[vehicles[i].ID] = vehicles[i].Paid
	}
	return m
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
	year := parseYearParam(r, now.Year())

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
	setFlatRateCoverage(resp.Vehicles, map[int64][]models.FlatRatePeriod{id: agreements}, now)

	// Rent = flat-rate agreements + per-vehicle cost of standalone vehicles.
	// Vehicles bound to a Pauschale bill nothing individually (ownership model).
	until := now.AddDate(0, 0, 1)
	rentAccrued, rentPaid := personRent(agreements, vehicles, cats, time.Time{}, until)

	vehPaid := vehiclePaidMap(vehicles)
	recurs, err := h.loadRecurringCharges(r.Context(), id, agreements, vehPaid, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	resp.RecurringCharges = recurs

	// Charts show total cost (rent + extra charges). Fold one-off charges (by date)
	// and recurring accrual into the per-month and per-year figures. Future-dated
	// one-off charges are excluded via until, matching rent/recurring.
	otMonth, otYear, err := h.chargeChartData(r.Context(), id, year, until)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	recMonth := recurringMonthly(recurs, year, now)
	resp.MonthlyAccrued = personMonthly(agreements, vehicles, cats, year, now)
	for m := range resp.MonthlyAccrued {
		resp.MonthlyAccrued[m] = round2(resp.MonthlyAccrued[m] + otMonth[m] + recMonth[m])
	}

	byYear := map[int]float64{}
	minYear := now.Year()
	for _, ys := range personYears(agreements, vehicles, cats, now) {
		byYear[ys.Year] += ys.Cost
		if ys.Year < minYear {
			minYear = ys.Year
		}
	}
	for y, c := range otYear {
		byYear[y] += c
		if y < minYear {
			minYear = y
		}
	}
	for i := range recurs {
		p := recurs[i].AsPeriod()
		if sy := p.StartDate.Year(); sy < minYear {
			minYear = sy
		}
		for y := p.StartDate.Year(); y <= now.Year(); y++ {
			yFrom := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
			yTo := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC)
			if yTo.After(until) {
				yTo = until
			}
			if yTo.After(yFrom) {
				byYear[y] += p.CostInRange(yFrom, yTo)
			}
		}
	}
	resp.Years = []models.YearStat{}
	for y := now.Year(); y >= minYear; y-- {
		if c := round2(byYear[y]); c > 0 {
			resp.Years = append(resp.Years, models.YearStat{Year: y, Cost: c})
		}
	}

	// Extra charges are billed on top of rent; a bound charge is paid when its
	// covering Pauschale's period is paid (or the vehicle's own paid flag is set),
	// a standalone charge when its own flag is set.
	totalCharges, paidCharges, err := h.personChargeSums(r.Context(), id, agreements, vehPaid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	recAccrued, recPaid := recurringSums(recurs, agreements, vehPaid, now)

	resp.TotalAccrued = round2(rentAccrued)
	resp.TotalCharges = round2(totalCharges + recAccrued)
	resp.TotalPaid = round2(rentPaid + paidCharges + recPaid)
	resp.Balance = round2(resp.TotalAccrued + resp.TotalCharges - resp.TotalPaid)

	// Recorded payments (money-in log) — independent of the per-item paid flags
	// above; powers the Kontoauszug.
	if perr := h.Pool.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(amount),0),
		        COALESCE(SUM(amount) FILTER (WHERE EXTRACT(YEAR FROM paid_on) = $2),0)
		   FROM payments WHERE person_id=$1`, id, year,
	).Scan(&resp.PaymentsTotal, &resp.PaymentsYear); perr == nil {
		resp.PaymentsTotal = round2(resp.PaymentsTotal)
		resp.PaymentsYear = round2(resp.PaymentsYear)
	}
	// Guthaben = true overpayment: money received beyond EVERYTHING owed (rent +
	// all charges + recurring), not the unallocated remainder — otherwise costs
	// that settle without an allocation (vehicle-bound charges, Pauschale/recurring
	// periods) would masquerade as credit.
	if resp.Credit = round2(resp.PaymentsTotal - (resp.TotalAccrued + resp.TotalCharges)); resp.Credit < 0 {
		resp.Credit = 0
	}

	writeJSON(w, http.StatusOK, resp)
}

// personAccruedTotal returns a person's total accrued cost (rent + one-off
// charges + recurring), the same "Aufgelaufen" PersonStats reports. Used where
// only the total is needed (Guthaben / apply-credit).
func (h *Handler) personAccruedTotal(r *http.Request, id int64) (float64, error) {
	ctx := r.Context()
	now := time.Now()
	vehicles, cats, err := h.loadVehiclesWithCategories(r, id)
	if err != nil {
		return 0, err
	}
	ags, err := h.loadAgreements(ctx, id, now)
	if err != nil {
		return 0, err
	}
	setFlatRateCoverage(vehicles, map[int64][]models.FlatRatePeriod{id: ags}, now)
	until := now.AddDate(0, 0, 1)
	rentAccrued, _ := personRent(ags, vehicles, cats, time.Time{}, until)
	vehPaid := vehiclePaidMap(vehicles)
	recurs, err := h.loadRecurringCharges(ctx, id, ags, vehPaid, now)
	if err != nil {
		return 0, err
	}
	totalCharges, _, err := h.personChargeSums(ctx, id, ags, vehPaid)
	if err != nil {
		return 0, err
	}
	recAccrued, _ := recurringSums(recurs, ags, vehPaid, now)
	return round2(rentAccrued + totalCharges + recAccrued), nil
}

// outstandingByPerson computes every person's open balance in one batched pass,
// so the persons list can show who owes without an N+1 of PersonStats. The money
// math is identical to PersonStats.Balance and the dashboard's TopOutstanding,
// expressed through the same shared helpers (personRent / chargeAmounts /
// recurringSums) — keep the three in sync. Returns person_id → open balance
// (≤ 0 means settled).
func (h *Handler) outstandingByPerson(r *http.Request) (map[int64]float64, error) {
	ctx := r.Context()
	vehicles, cats, err := h.loadVehiclesWithCategories(r, 0)
	if err != nil {
		return nil, err
	}
	agByPerson, err := h.loadAllAgreements(ctx, 0)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	until := now.AddDate(0, 0, 1)
	vehPaid := vehiclePaidMap(vehicles)

	vehByPerson := map[int64][]models.Vehicle{}
	for i := range vehicles {
		v := &vehicles[i]
		vehByPerson[v.PersonID] = append(vehByPerson[v.PersonID], *v)
	}

	// One-off charges per person (total accrued + paid).
	chargesByPerson := map[int64]float64{}
	paidChargesByPerson := map[int64]float64{}
	crows, err := h.Pool.Query(ctx, `SELECT person_id, vehicle_id, amount, quantity, charged_on, paid FROM charges`)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var pid int64
		var vid *int64
		var amount, qty float64
		var chargedOn time.Time
		var ownPaid bool
		if serr := crows.Scan(&pid, &vid, &amount, &qty, &chargedOn, &ownPaid); serr != nil {
			crows.Close()
			return nil, serr
		}
		t, p := chargeAmounts(agByPerson[pid], vehPaid, vid, amount, qty, chargedOn, ownPaid)
		chargesByPerson[pid] += t
		paidChargesByPerson[pid] += p
	}
	crows.Close()
	if cerr := crows.Err(); cerr != nil {
		return nil, cerr
	}

	// Recurring extra costs accrue into the same charge totals.
	recurByPerson, err := h.loadAllRecurringCharges(ctx, agByPerson, vehPaid, now)
	if err != nil {
		return nil, err
	}
	for pid, list := range recurByPerson {
		acc, pd := recurringSums(list, agByPerson[pid], vehPaid, now)
		chargesByPerson[pid] += acc
		paidChargesByPerson[pid] += pd
	}

	// Any person with vehicles, an agreement, or a charge can carry a balance.
	personIDs := map[int64]struct{}{}
	for pid := range vehByPerson {
		personIDs[pid] = struct{}{}
	}
	for pid := range agByPerson {
		personIDs[pid] = struct{}{}
	}
	for pid := range chargesByPerson {
		personIDs[pid] = struct{}{}
	}

	out := make(map[int64]float64, len(personIDs))
	for pid := range personIDs {
		tAcc, tPaid := personRent(agByPerson[pid], vehByPerson[pid], cats, time.Time{}, until)
		owed := (tAcc + chargesByPerson[pid]) - (tPaid + paidChargesByPerson[pid])
		out[pid] = round2(owed)
	}
	return out, nil
}

// OutstandingByPerson returns a person_id → open-balance map for the persons
// list, so each row can show its settlement status without loading per-person
// stats one at a time.
func (h *Handler) OutstandingByPerson(w http.ResponseWriter, r *http.Request) {
	out, err := h.outstandingByPerson(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// overviewResponse summarizes the whole dataset for the dashboard.
type overviewResponse struct {
	TotalPersons     int            `json:"total_persons"`
	TotalVehicles    int            `json:"total_vehicles"`
	ActiveVehicles   int            `json:"active_vehicles"`
	TotalCategories  int            `json:"total_categories"`
	AccruedThisYear  float64        `json:"accrued_this_year"`
	AccruedPrevYear  float64        `json:"accrued_prev_year"` // same window one year earlier, for a YoY delta
	AccruedPrevFull  float64        `json:"accrued_prev_full"` // full previous calendar year, for "% of last year" / projection modes
	AccruedTotal     float64        `json:"accrued_total"`
	PaidTotal        float64        `json:"paid_total"`
	OutstandingTotal float64        `json:"outstanding_total"`
	Year             int            `json:"year"`
	StatusCounts     map[string]int `json:"status_counts"`
	RevenueByMonth   []float64      `json:"revenue_by_month"`
	ChargesByMonth   []float64      `json:"charges_by_month"`
	PaymentsThisYear float64        `json:"payments_this_year"` // recorded money-in in the selected year
	PaymentsTotal    float64        `json:"payments_total"`     // recorded money-in (all time)
	PaymentsByMonth  []float64      `json:"payments_by_month"`  // recorded money-in per month of the selected year
	// TopOutstanding lists the persons with the largest open balance, so the
	// dashboard can point straight at who to follow up with.
	TopOutstanding []personOutstanding `json:"top_outstanding"`
}

// personOutstanding is one entry in the dashboard's "top open balances" list.
type personOutstanding struct {
	PersonID    int64   `json:"person_id"`
	Name        string  `json:"name"`
	Outstanding float64 `json:"outstanding"`
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
	resp.Year = parseYearParam(r, now.Year())
	resp.TopOutstanding = []personOutstanding{}
	yearStart := time.Date(resp.Year, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEnd := time.Date(resp.Year+1, 1, 1, 0, 0, 0, 0, time.UTC)
	until := now.AddDate(0, 0, 1)
	if yearEnd.After(until) {
		yearEnd = until
	}
	// Prior-year window of the same length (year-to-date vs same period last
	// year for the current year; full year vs full year for a past one).
	prevStart := yearStart.AddDate(-1, 0, 0)
	prevEnd := prevStart.Add(yearEnd.Sub(yearStart))

	personNames := map[int64]string{}
	if rows, nerr := h.Pool.Query(ctx, `SELECT id, trim(first_name || ' ' || last_name) FROM persons`); nerr == nil {
		defer rows.Close()
		for rows.Next() {
			var pid int64
			var name string
			if rows.Scan(&pid, &name) == nil {
				personNames[pid] = name
			}
		}
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
	agByPerson, err := h.loadAllAgreements(ctx, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Per-person charge sums (all + paid). A vehicle-bound charge is settled via
	// its covering Pauschale's period (or the vehicle's own paid flag); a
	// standalone charge via its own flag. Reused for the totals and open balances.
	crows, err := h.Pool.Query(ctx, `SELECT person_id, vehicle_id, amount, quantity, charged_on, paid FROM charges`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	vehPaid := vehiclePaidMap(vehicles)
	chargesByPerson := map[int64]float64{}
	paidChargesByPerson := map[int64]float64{}
	// Extra charges are revenue too: accrue the one-off charges into the year
	// windows and per month (by charge date) to fold into the Umsatz figures.
	var chargeThisYear, chargePrevYear, chargePrevFull float64
	chargeByMonth := make([]float64, 12)
	for crows.Next() {
		var pid int64
		var vid *int64
		var amount, qty float64
		var chargedOn time.Time
		var ownPaid bool
		if serr := crows.Scan(&pid, &vid, &amount, &qty, &chargedOn, &ownPaid); serr != nil {
			crows.Close()
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		t, p := chargeAmounts(agByPerson[pid], vehPaid, vid, amount, qty, chargedOn, ownPaid)
		chargesByPerson[pid] += t
		paidChargesByPerson[pid] += p
		if !chargedOn.Before(yearStart) && chargedOn.Before(yearEnd) {
			chargeThisYear += t
			chargeByMonth[int(chargedOn.Month())-1] += t
		}
		if !chargedOn.Before(prevStart) && chargedOn.Before(prevEnd) {
			chargePrevYear += t
		}
		if !chargedOn.Before(prevStart) && chargedOn.Before(yearStart) {
			chargePrevFull += t
		}
	}
	crows.Close()
	if cerr := crows.Err(); cerr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Recurring extra costs accrue per period into the same charge totals.
	recurByPerson, err := h.loadAllRecurringCharges(ctx, agByPerson, vehPaid, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	for pid, list := range recurByPerson {
		acc, pd := recurringSums(list, agByPerson[pid], vehPaid, now)
		chargesByPerson[pid] += acc
		paidChargesByPerson[pid] += pd
		for i := range list {
			p := list[i].AsPeriod()
			chargeThisYear += p.CostInRange(yearStart, yearEnd)
			chargePrevYear += p.CostInRange(prevStart, prevEnd)
			chargePrevFull += p.CostInRange(prevStart, yearStart)
		}
		recM := recurringMonthly(list, resp.Year, now)
		for m := range chargeByMonth {
			chargeByMonth[m] += recM[m]
		}
	}

	personIDs := map[int64]struct{}{}
	for pid := range vehByPerson {
		personIDs[pid] = struct{}{}
	}
	for pid := range agByPerson {
		personIDs[pid] = struct{}{}
	}
	// A person can owe money on charges alone (no vehicles/agreements).
	for pid := range chargesByPerson {
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
		pAcc, _ := personRent(ag, vs, cats, prevStart, prevEnd)
		resp.AccruedPrevYear += pAcc
		fAcc, _ := personRent(ag, vs, cats, prevStart, yearStart)
		resp.AccruedPrevFull += fAcc
		pm := personMonthly(ag, vs, cats, resp.Year, now)
		for m := range resp.RevenueByMonth {
			resp.RevenueByMonth[m] = round2(resp.RevenueByMonth[m] + pm[m])
		}
		// Open balance per person = (accrued rent + charges) − (paid rent + paid
		// charges), mirroring the person page's balance.
		owed := (tAcc + chargesByPerson[pid]) - (tPaid + paidChargesByPerson[pid])
		if owed > 0.005 {
			name := personNames[pid]
			if name == "" {
				name = "Person"
			}
			resp.TopOutstanding = append(resp.TopOutstanding, personOutstanding{
				PersonID: pid, Name: name, Outstanding: round2(owed),
			})
		}
	}
	// Revenue (Umsatz) = rent + extra charges. Fold charges into the accrued
	// totals; the per-person charge maps carry the whole-period totals.
	var totalCharges, paidCharges float64
	for _, v := range chargesByPerson {
		totalCharges += v
	}
	for _, v := range paidChargesByPerson {
		paidCharges += v
	}
	resp.AccruedTotal = round2(resp.AccruedTotal + totalCharges)
	resp.AccruedThisYear = round2(resp.AccruedThisYear + chargeThisYear)
	resp.AccruedPrevYear = round2(resp.AccruedPrevYear + chargePrevYear)
	resp.AccruedPrevFull = round2(resp.AccruedPrevFull + chargePrevFull)
	for m := range resp.RevenueByMonth {
		resp.RevenueByMonth[m] = round2(resp.RevenueByMonth[m] + chargeByMonth[m])
	}

	// Largest open balances first, capped so the dashboard stays a summary.
	sort.Slice(resp.TopOutstanding, func(i, j int) bool {
		return resp.TopOutstanding[i].Outstanding > resp.TopOutstanding[j].Outstanding
	})
	if len(resp.TopOutstanding) > 5 {
		resp.TopOutstanding = resp.TopOutstanding[:5]
	}

	// Extra charges per month (one-off + recurring), kept as a separate chart.
	resp.ChargesByMonth = make([]float64, 12)
	for m := range chargeByMonth {
		resp.ChargesByMonth[m] = round2(chargeByMonth[m])
	}

	resp.PaidTotal = round2(paidRent + paidCharges)
	resp.OutstandingTotal = round2(resp.AccruedTotal - resp.PaidTotal)

	// Recorded payments (money-in log): total, this-year, and per month for the
	// selected year — independent of the paid-flag balance math above.
	resp.PaymentsByMonth = make([]float64, 12)
	if prows, perr := h.Pool.Query(ctx, `SELECT amount, paid_on FROM payments`); perr == nil {
		defer prows.Close()
		for prows.Next() {
			var amt float64
			var on time.Time
			if prows.Scan(&amt, &on) == nil {
				resp.PaymentsTotal += amt
				if !on.Before(yearStart) && on.Before(yearEnd) {
					resp.PaymentsThisYear += amt
					resp.PaymentsByMonth[int(on.Month())-1] += amt
				}
			}
		}
	}
	resp.PaymentsTotal = round2(resp.PaymentsTotal)
	resp.PaymentsThisYear = round2(resp.PaymentsThisYear)
	for m := range resp.PaymentsByMonth {
		resp.PaymentsByMonth[m] = round2(resp.PaymentsByMonth[m])
	}

	writeJSON(w, http.StatusOK, resp)
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
