package handlers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// reconcileMonthsToYear nudges the 12 monthly values (euros) so they sum EXACTLY
// to the year total, moving the small per-month proration residual onto the
// largest months (largest-remainder). Display-only: the Balance/year card use the
// exact whole-span figure; this just keeps the "Umsatz nach Monat" bars summing
// to it (independent per-month rounding of a yearly-billed item drifts a cent or two).
func reconcileMonthsToYear(months []float64, yearTotal float64) {
	cents := make([]int64, len(months))
	var sum int64
	for i, m := range months {
		cents[i] = int64(math.Round(m * 100))
		sum += cents[i]
	}
	residual := int64(math.Round(yearTotal*100)) - sum
	if residual == 0 || len(months) == 0 {
		return
	}
	order := make([]int, len(months))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return cents[order[a]] > cents[order[b]] })
	step, n := int64(1), residual
	if n < 0 {
		step, n = -1, -n
	}
	for k := int64(0); k < n; k++ {
		cents[order[int(k)%len(order)]] += step
	}
	for i := range months {
		months[i] = float64(cents[i]) / 100
	}
}

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
	InvoicedTax      float64                  `json:"invoiced_tax"`   // USt added by issued invoices (owed on top of net accrual)
	PeriodPaid       float64                  `json:"period_paid"`    // per-period Pauschale/recurring settlement (not in payments)
	InvoicedOpen     float64                  `json:"invoiced_open"`  // Σ open_amount of the person's non-canceled invoices (OP)
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
// paid return is currently unused by the balance (charges settle via the payment
// path) but is kept as the symmetric settlement helper alongside recurringPaidBound.
//
//nolint:unparam // paid retained as the settlement API; see comment above
func chargeAmounts(agreements []models.FlatRatePeriod, vehPaid map[int64]bool, vid *int64, amount, qty float64, chargedOn time.Time, ownPaid bool) (total, paid float64) {
	// Round each charge's line total to the cent, exactly as the invoice and the
	// payment path do (round2(amount*qty)) — and normalize qty<=0 → 1 the same way
	// invoiceLines does, so the two can't diverge into a phantom Guthaben.
	if qty <= 0 {
		qty = 1
	}
	total = round2(amount * qty)
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
func (h *Handler) personChargeSums(ctx context.Context, personID int64, agreements []models.FlatRatePeriod, vehPaid map[int64]bool) (total float64, err error) {
	rows, qerr := h.Pool.Query(ctx,
		`SELECT vehicle_id, amount, quantity, charged_on, paid FROM charges WHERE person_id=$1`, personID)
	if qerr != nil {
		return 0, qerr
	}
	defer rows.Close()
	for rows.Next() {
		var vid *int64
		var amount, qty float64
		var chargedOn time.Time
		var ownPaid bool
		if serr := rows.Scan(&vid, &amount, &qty, &chargedOn, &ownPaid); serr != nil {
			return 0, serr
		}
		t, _ := chargeAmounts(agreements, vehPaid, vid, amount, qty, chargedOn, ownPaid)
		total += t
	}
	return total, rows.Err()
}

// personPeriodPaid sums money settled through the per-period Pauschale / recurring
// mechanism (agreement + recurring paid_periods / paid_fixed). Unlike vehicle and
// one-off-charge toggles — which record a P2.3 auto-payment — this money is NOT in
// the payments table, so the payment-based balance must subtract it explicitly.
// Otherwise a paid period is owed forever (the invoice already omits it) — an
// uncollectable phantom debt. Vehicle rent settled via v.Paid is deliberately
// excluded here: it already has a payment row and would double-count.
func personPeriodPaid(agreements []models.FlatRatePeriod, recurs []models.RecurringCharge, now time.Time, locked map[string]bool) float64 {
	var paid float64
	for i := range agreements {
		a := &agreements[i]
		paid += periodPaidUnlocked(*a, "agreement", a.ID, now, locked)
	}
	for i := range recurs {
		paid += periodPaidUnlocked(recurs[i].AsPeriod(), "recurring", recurs[i].ID, now, locked)
	}
	return round2(paid)
}

// periodPaidUnlocked sums one accrual's per-period paid amount, EXCLUDING sub-
// periods already billed by an active invoice (present in invoice_source / locked).
// An invoiced period is settled through the invoice payment, so crediting it here
// too would double-count (phantom Guthaben). A fixed partial is capped at the
// period's accrued-so-far cost, so a prepayment on a still-running period can't
// over-credit and drive the balance transiently negative.
func periodPaidUnlocked(p models.FlatRatePeriod, kind string, refID int64, now time.Time, locked map[string]bool) float64 {
	paidSet := periodKeySet(p.PaidPeriods)
	var paid float64
	for _, per := range p.ElapsedPeriodsDetailed(now) {
		if locked[lockKey(kind, refID, per.Key)] {
			// The period was invoiced. A FULLY-paid period is never invoiced (its
			// open amount is ≤0, so no lock), so a locked period can only carry a
			// fixed PARTIAL: the invoice billed cost − PaidFixed, and that off-book
			// partial is still real money. Keep crediting it (capped at cost), else
			// it becomes phantom debt equal to the partial once the invoice is paid.
			if fx, ok := p.PaidFixed[per.Key]; ok {
				if fx < per.Cost {
					paid += fx
				} else {
					paid += per.Cost
				}
			}
			continue
		}
		switch {
		case p.Paid || paidSet[per.Key]:
			paid += per.Cost
		default:
			if fx, ok := p.PaidFixed[per.Key]; ok {
				if fx < per.Cost {
					paid += fx
				} else {
					paid += per.Cost
				}
			}
		}
	}
	return paid
}

// lockedPeriodsByPerson returns, per person, the set of invoiced (locked) position
// keys from active invoices — the bulk equivalent of lockedPositions, so the
// dashboard/outstanding paths can exclude invoiced periods from the off-book
// per-period credit (see personPeriodPaid) without a query per person.
// sumByPerson runs a "person_id, SUM(...)" grouped query and returns it as a map,
// surfacing any error instead of silently yielding zeros — a swallowed error here
// skews every person's dashboard receivable.
func sumByPerson(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, sql string) (map[int64]float64, error) {
	out := map[int64]float64{}
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		var amt float64
		if err := rows.Scan(&pid, &amt); err != nil {
			return nil, err
		}
		out[pid] = amt
	}
	return out, rows.Err()
}

func (h *Handler) lockedPeriodsByPerson(ctx context.Context) (map[int64]map[string]bool, error) {
	out := map[int64]map[string]bool{}
	rows, err := h.Pool.Query(ctx,
		`SELECT i.person_id, s.kind, s.ref_id, s.period_key FROM invoice_source s
		    JOIN invoices i ON i.id = s.invoice_id WHERE NOT i.canceled`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid, ref int64
		var kind, period string
		if err := rows.Scan(&pid, &kind, &ref, &period); err != nil {
			return nil, err
		}
		if out[pid] == nil {
			out[pid] = map[string]bool{}
		}
		out[pid][lockKey(kind, ref, period)] = true
	}
	// A mid-stream error would drop invoiced periods → double-credit → understate
	// owed; surface it rather than serving confidently-wrong dashboard money.
	return out, rows.Err()
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
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "person not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "query failed")
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
	until := models.DayAfter(now)
	rentAccrued := personRent(agreements, vehicles, cats, time.Time{}, until)

	vehPaid := vehiclePaidMap(vehicles)
	recurs, err := h.loadRecurringCharges(r.Context(), id, now)
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
	// Make the monthly bars sum exactly to the selected year's total (S1).
	reconcileMonthsToYear(resp.MonthlyAccrued, round2(byYear[year]))

	// Extra charges are billed on top of rent; a bound charge is paid when its
	// covering Pauschale's period is paid (or the vehicle's own paid flag is set),
	// a standalone charge when its own flag is set.
	totalCharges, err := h.personChargeSums(r.Context(), id, agreements, vehPaid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	recAccrued, _ := recurringSums(recurs, agreements, vehPaid, now)

	resp.TotalAccrued = round2(rentAccrued)
	resp.TotalCharges = round2(totalCharges + recAccrued)

	// Recorded payments (money actually received). Powers the Kontoauszug.
	// Don't swallow: a dropped payments read would leave PaymentsTotal 0 and
	// overstate the receivable (Balance = owed − received).
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(amount),0),
		        COALESCE(SUM(amount) FILTER (WHERE EXTRACT(YEAR FROM paid_on) = $2),0)
		   FROM payments WHERE person_id=$1 AND NOT reversed`, id, year,
	).Scan(&resp.PaymentsTotal, &resp.PaymentsYear); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	resp.PaymentsTotal = round2(resp.PaymentsTotal)
	resp.PaymentsYear = round2(resp.PaymentsYear)

	// Accrual is NET; an issued invoice adds USt on top and the customer pays that
	// GROSS. Add the invoiced tax to the owed side so a paid USt-invoice doesn't
	// look like Guthaben equal to its tax. Kleinunternehmer => tax 0 (no-op).
	var invoicedTax float64
	// Don't swallow this: a silent 0 would drop the USt the customer owes on issued
	// invoices, making a fully-paid USt-invoice look like Guthaben equal to its tax.
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(tax_amount),0) FROM invoices
		   WHERE person_id=$1 AND NOT canceled AND cancels_id IS NULL`, id).Scan(&invoicedTax); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// P2.2 — one money truth: paid = money actually received; Balance = owed −
	// received (the per-item toggles are a manual convenience, not the money).
	resp.TotalPaid = resp.PaymentsTotal
	resp.InvoicedTax = round2(invoicedTax)
	// Exclude invoiced periods (settled via the invoice payment) from the off-book
	// per-period credit so a period isn't credited twice. A read error here would
	// double-credit invoiced periods → understate owed, so surface it.
	periodLocked, lerr := h.lockedPositions(r.Context(), id)
	if lerr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	resp.PeriodPaid = personPeriodPaid(agreements, recurs, now, periodLocked)
	resp.Balance = round2(resp.TotalAccrued + resp.TotalCharges + resp.InvoicedTax -
		resp.PaymentsTotal - resp.PeriodPaid)
	// Guthaben = overpayment (negative balance), symmetric to Balance.
	if resp.Credit = round2(-resp.Balance); resp.Credit < 0 {
		resp.Credit = 0
	}
	// Offen fakturiert: the sum of the person's open invoices (the OP view).
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(total - paid_amount),0) FROM invoices
		   WHERE person_id=$1 AND NOT canceled AND cancels_id IS NULL AND (total - paid_amount) > 0.005`, id,
	).Scan(&resp.InvoicedOpen); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	resp.InvoicedOpen = round2(resp.InvoicedOpen)

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
	until := models.DayAfter(now)
	rentAccrued := personRent(ags, vehicles, cats, time.Time{}, until)
	vehPaid := vehiclePaidMap(vehicles)
	recurs, err := h.loadRecurringCharges(ctx, id, now)
	if err != nil {
		return 0, err
	}
	totalCharges, err := h.personChargeSums(ctx, id, ags, vehPaid)
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
	until := models.DayAfter(now)
	vehPaid := vehiclePaidMap(vehicles)

	vehByPerson := map[int64][]models.Vehicle{}
	for i := range vehicles {
		v := &vehicles[i]
		vehByPerson[v.PersonID] = append(vehByPerson[v.PersonID], *v)
	}

	// One-off charges per person (total accrued; the paid portion no longer drives
	// the payment-based balance, so it is not accumulated).
	chargesByPerson := map[int64]float64{}
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
		t, _ := chargeAmounts(agByPerson[pid], vehPaid, vid, amount, qty, chargedOn, ownPaid)
		chargesByPerson[pid] += t
	}
	crows.Close()
	if cerr := crows.Err(); cerr != nil {
		return nil, cerr
	}

	// Recurring extra costs accrue into the same charge totals.
	recurByPerson, err := h.loadAllRecurringCharges(ctx, now)
	if err != nil {
		return nil, err
	}
	for pid, list := range recurByPerson {
		acc, _ := recurringSums(list, agByPerson[pid], vehPaid, now)
		chargesByPerson[pid] += acc
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

	// P2.2 — open = accrued − payments received (the money truth), per person.
	// Don't fail open: a swallowed error would drop everyone's payments and
	// overstate every receivable on the dashboard.
	paymentsByPerson, err := sumByPerson(ctx, h.Pool,
		`SELECT person_id, COALESCE(SUM(amount),0) FROM payments WHERE NOT reversed GROUP BY person_id`)
	if err != nil {
		return nil, err
	}

	// Invoiced USt is owed on top of the net accrual (see PersonStats).
	invoicedTaxByPerson, err := sumByPerson(ctx, h.Pool,
		`SELECT person_id, COALESCE(SUM(tax_amount),0) FROM invoices WHERE NOT canceled AND cancels_id IS NULL GROUP BY person_id`)
	if err != nil {
		return nil, err
	}

	// A person owing only a standing invoice (e.g. USt after the underlying charge
	// was deleted) must still count — seed from the invoiced set too.
	for pid := range invoicedTaxByPerson {
		personIDs[pid] = struct{}{}
	}
	lockedByPerson, lerr := h.lockedPeriodsByPerson(ctx)
	if lerr != nil {
		return nil, lerr
	}

	out := make(map[int64]float64, len(personIDs))
	for pid := range personIDs {
		tAcc := personRent(agByPerson[pid], vehByPerson[pid], cats, time.Time{}, until)
		owed := (tAcc + chargesByPerson[pid] + invoicedTaxByPerson[pid]) - paymentsByPerson[pid] -
			personPeriodPaid(agByPerson[pid], recurByPerson[pid], now, lockedByPerson[pid])
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
	until := models.DayAfter(now)
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
		t, _ := chargeAmounts(agByPerson[pid], vehPaid, vid, amount, qty, chargedOn, ownPaid)
		chargesByPerson[pid] += t
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
	recurByPerson, err := h.loadAllRecurringCharges(ctx, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	for pid, list := range recurByPerson {
		acc, _ := recurringSums(list, agByPerson[pid], vehPaid, now)
		chargesByPerson[pid] += acc
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
	// P2.2 — money truth = recorded payments (per person + total).
	paymentsByPerson, perr := sumByPerson(ctx, h.Pool,
		`SELECT person_id, COALESCE(SUM(amount),0) FROM payments WHERE NOT reversed GROUP BY person_id`)
	if perr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	var paymentsTotal float64
	for _, v := range paymentsByPerson {
		paymentsTotal += v
	}

	// Invoiced USt owed on top of net accrual (see PersonStats).
	invoicedTaxByPerson, terr := sumByPerson(ctx, h.Pool,
		`SELECT person_id, COALESCE(SUM(tax_amount),0) FROM invoices WHERE NOT canceled AND cancels_id IS NULL GROUP BY person_id`)
	if terr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	// Count a person owing only a standing invoice (e.g. USt after the charge was
	// deleted); exclude invoiced periods from the off-book per-period credit.
	for pid := range invoicedTaxByPerson {
		personIDs[pid] = struct{}{}
	}
	lockedByPerson, lerr := h.lockedPeriodsByPerson(ctx)
	if lerr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	resp.RevenueByMonth = make([]float64, 12)
	var outstandingSum float64 // Σ of positive per-person balances (real receivable)
	for pid := range personIDs {
		ag, vs := agByPerson[pid], vehByPerson[pid]
		tAcc := personRent(ag, vs, cats, time.Time{}, until)
		resp.AccruedTotal += tAcc
		yAcc := personRent(ag, vs, cats, yearStart, yearEnd)
		resp.AccruedThisYear += yAcc
		pAcc := personRent(ag, vs, cats, prevStart, prevEnd)
		resp.AccruedPrevYear += pAcc
		fAcc := personRent(ag, vs, cats, prevStart, yearStart)
		resp.AccruedPrevFull += fAcc
		pm := personMonthly(ag, vs, cats, resp.Year, now)
		for m := range resp.RevenueByMonth {
			resp.RevenueByMonth[m] = round2(resp.RevenueByMonth[m] + pm[m])
		}
		// Open balance per person = owed (rent + charges + invoiced USt) − payments −
		// per-period Pauschale/recurring settlement (not in the payments table).
		owed := (tAcc + chargesByPerson[pid] + invoicedTaxByPerson[pid]) - paymentsByPerson[pid] -
			personPeriodPaid(ag, recurByPerson[pid], now, lockedByPerson[pid])
		if owed > 0.005 {
			outstandingSum += owed
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
	var totalCharges float64
	for _, v := range chargesByPerson {
		totalCharges += v
	}
	resp.AccruedTotal = round2(resp.AccruedTotal + totalCharges)
	resp.AccruedThisYear = round2(resp.AccruedThisYear + chargeThisYear)
	resp.AccruedPrevYear = round2(resp.AccruedPrevYear + chargePrevYear)
	resp.AccruedPrevFull = round2(resp.AccruedPrevFull + chargePrevFull)
	for m := range resp.RevenueByMonth {
		resp.RevenueByMonth[m] = round2(resp.RevenueByMonth[m] + chargeByMonth[m])
	}
	// Bars sum exactly to the year's accrual (rent + charges), same composition (S1).
	reconcileMonthsToYear(resp.RevenueByMonth, resp.AccruedThisYear)

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

	resp.PaidTotal = round2(paymentsTotal)
	// Real receivable = sum of positive per-person balances, NOT accrued − payments
	// (which would net overpayers' Guthaben against debtors and understate it, and
	// disagree with the TopOutstanding list).
	resp.OutstandingTotal = round2(outstandingSum)

	// Recorded payments (money-in log): total, this-year, and per month for the
	// selected year — independent of the paid-flag balance math above.
	resp.PaymentsByMonth = make([]float64, 12)
	prows, perr := h.Pool.Query(ctx, `SELECT amount, paid_on FROM payments WHERE NOT reversed`)
	if perr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	for prows.Next() {
		var amt float64
		var on time.Time
		if err := prows.Scan(&amt, &on); err != nil {
			prows.Close()
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		resp.PaymentsTotal += amt
		if !on.Before(yearStart) && on.Before(yearEnd) {
			resp.PaymentsThisYear += amt
			resp.PaymentsByMonth[int(on.Month())-1] += amt
		}
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
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
	until := models.DayAfter(now)
	for m := 0; m < 12; m++ {
		from := time.Date(year, time.Month(m+1), 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 1, 0)
		if to.After(until) {
			to = until
		}
		if !to.After(from) {
			continue
		}
		acc := personRent(agreements, vehicles, cats, from, to)
		out[m] = round2(acc)
	}
	return out
}

// personYears computes combined rent per calendar year, newest first, from the
// earliest agreement/vehicle start through today.
func personYears(agreements []models.FlatRatePeriod, vehicles []models.Vehicle, cats map[int64]models.Category, now time.Time) []models.YearStat {
	res := []models.YearStat{}
	until := models.DayAfter(now)
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
		acc := personRent(agreements, vehicles, cats, from, to)
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
	if err := h.setVehicleInvoiceStatus(r.Context(), vehicles); err != nil {
		return nil, nil, err
	}
	return vehicles, cats, nil
}
