package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

type dashboardYears struct {
	Available []int
	Present   map[int]bool
	loadedAt  time.Time
}

func newDashboardYears(now time.Time) dashboardYears {
	return dashboardYears{Available: []int{now.Year()}, Present: map[int]bool{}, loadedAt: time.Now()}
}

// Availability records existence, not positive revenue. A free rental, archived
// rental or reversed receipt is still business history; gaps remain real gaps.
func (ys *dashboardYears) addSpan(start time.Time, end *time.Time, now time.Time) {
	until := models.DayAfter(now)
	if end != nil && models.DayAfter(*end).Before(until) {
		until = models.DayAfter(*end)
	}
	if !until.After(start) {
		return
	}
	first, last := max(2000, start.Year()), min(now.Year(), until.AddDate(0, 0, -1).Year())
	for year := first; year <= last && year <= 2100; year++ {
		ys.Present[year] = true
	}
}

func (ys *dashboardYears) finish(currentYear int) {
	ys.Available = []int{}
	for year := range ys.Present {
		ys.Available = append(ys.Available, year)
	}
	if !ys.Present[currentYear] {
		ys.Available = append(ys.Available, currentYear)
	}
	slices.Sort(ys.Available)
}

func (ys dashboardYears) canonical(requested, currentYear int, allowNext bool) int {
	nextAllowed := allowNext && currentYear < 2100 && requested == currentYear+1
	if requested == currentYear || nextAllowed || ys.Present[requested] {
		return requested
	}
	return currentYear
}

func loadDashboardYears(ctx context.Context, q dbQuerier, now time.Time) (dashboardYears, error) {
	ys := newDashboardYears(now)
	rows, err := q.Query(ctx, `
		SELECT start_date, end_date FROM vehicles
		UNION ALL SELECT start_date, end_date FROM flat_rate_periods
		UNION ALL SELECT start_date, end_date FROM recurring_charges
		UNION ALL SELECT charged_on, charged_on FROM charges
		UNION ALL SELECT paid_on, paid_on FROM payments`)
	if err != nil {
		return ys, fmt.Errorf("load dashboard years: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var start time.Time
		var end *time.Time
		if err := rows.Scan(&start, &end); err != nil {
			return ys, fmt.Errorf("scan dashboard years: %w", err)
		}
		ys.addSpan(start, end, now)
	}
	if err := rows.Err(); err != nil {
		return ys, fmt.Errorf("read dashboard years: %w", err)
	}
	ys.finish(now.Year())
	return ys, nil
}

type dashboardForecastYear struct {
	Year         int     `json:"year"`
	Actual       float64 `json:"actual"`
	Forecast     float64 `json:"forecast"`
	Reservations float64 `json:"reservations"`
	HasData      bool    `json:"has_data"`
}

type dashboardForecastResponse struct {
	Year                int                     `json:"year"`
	CurrentYear         int                     `json:"current_year"`
	AsOf                string                  `json:"as_of"`
	AvailableYears      []int                   `json:"available_years"`
	ForecastYears       []int                   `json:"forecast_years"`
	HasYearData         bool                    `json:"has_year_data"`
	ActualByMonth       []float64               `json:"actual_by_month"`
	ForecastByMonth     []float64               `json:"forecast_by_month"`
	ReservationsByMonth []float64               `json:"reservations_by_month"`
	PreviousByMonth     []float64               `json:"previous_by_month"` // nil means unavailable, not zero.
	Years               []dashboardForecastYear `json:"years"`
	Assumptions         []string                `json:"assumptions"`
	Warnings            []string                `json:"warnings"`
}

type dashboardForecastCharge struct {
	Amount float64
	On     time.Time
}

type dashboardForecastData struct {
	Vehicles   []models.Vehicle
	Categories map[int64]models.Category
	Agreements map[int64][]models.FlatRatePeriod
	Recurring  []models.FlatRatePeriod
	Charges    []dashboardForecastCharge
}

// OverviewForecast is a read-only contract-accrual scenario, never a payment,
// invoice, tax calculation or promise that a reservation will be fulfilled.
func (h *Handler) OverviewForecast(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	now := h.now()
	requested := parseYearParam(r, now.Year()) // Bound input before reading any business data.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rep, err := readDashboardForecast(ctx, h.Pool, requested, now)
	if err != nil {
		serverError(w, r, "Die Bestandsprognose konnte nicht geladen werden.", err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func readDashboardForecast(
	ctx context.Context,
	db taxExportBeginner,
	requested int,
	now time.Time,
) (rep dashboardForecastResponse, err error) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return rep, fmt.Errorf("begin forecast snapshot: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(cleanupCtx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("rollback forecast snapshot: %w", rollbackErr))
		}
	}()
	ys, err := loadDashboardYears(ctx, tx, now)
	if err != nil {
		return rep, err
	}
	data, err := loadDashboardForecastData(ctx, tx)
	if err != nil {
		return rep, err
	}
	// Release the connection before the bounded, potentially CPU-heavy chart work.
	if err := tx.Commit(ctx); err != nil {
		return rep, fmt.Errorf("close forecast snapshot: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	rep = buildDashboardForecast(data, ys, requested, now)
	return rep, ctx.Err()
}

// Forecast reads only principal amounts and dates. Payment flags and invoice
// settlement joins cannot remove already-earned rent or introduce cash twice.
func loadDashboardForecastData(ctx context.Context, q dbQuerier) (dashboardForecastData, error) {
	data := dashboardForecastData{
		Vehicles: []models.Vehicle{}, Categories: map[int64]models.Category{},
		Agreements: map[int64][]models.FlatRatePeriod{},
		Recurring:  []models.FlatRatePeriod{}, Charges: []dashboardForecastCharge{},
	}
	if err := loadForecastVehicles(ctx, q, &data); err != nil {
		return data, err
	}
	if err := loadForecastAgreements(ctx, q, &data); err != nil {
		return data, err
	}
	if err := loadForecastRecurring(ctx, q, &data); err != nil {
		return data, err
	}
	rows, err := q.Query(ctx, `SELECT amount, quantity, charged_on FROM charges ORDER BY id`)
	if err != nil {
		return data, fmt.Errorf("load forecast charges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var charge dashboardForecastCharge
		var amount, quantity float64
		if err := rows.Scan(&amount, &quantity, &charge.On); err != nil {
			return data, fmt.Errorf("scan forecast charge: %w", err)
		}
		charge.Amount, _ = chargeAmounts(nil, nil, nil, amount, quantity, charge.On, false)
		data.Charges = append(data.Charges, charge)
	}
	return data, rows.Err()
}

func loadForecastVehicles(ctx context.Context, q dbQuerier, data *dashboardForecastData) error {
	rows, err := q.Query(ctx, `
		SELECT v.id, v.person_id, v.category_id, v.billing_period, v.rate, v.cost_override,
		       v.start_date, v.end_date, v.status, v.archived, v.reserved_from, v.reserved_until,
		       c.default_monthly_cost, c.default_yearly_cost,
		       (SELECT jsonb_agg(jsonb_build_object('from', to_char(rc.effective_from, 'YYYY-MM-DD"T"00:00:00"Z"'), 'amount', rc.rate)
		               ORDER BY rc.effective_from) FROM vehicle_rate_changes rc WHERE rc.vehicle_id = v.id)
		  FROM vehicles v JOIN categories c ON c.id=v.category_id ORDER BY v.id`)
	if err != nil {
		return fmt.Errorf("load forecast vehicles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var vehicle models.Vehicle
		var category models.Category
		var rateChanges []byte
		if err := rows.Scan(
			&vehicle.ID, &vehicle.PersonID, &vehicle.CategoryID, &vehicle.BillingPeriod,
			&vehicle.Rate, &vehicle.CostOverride, &vehicle.StartDate, &vehicle.EndDate,
			&vehicle.Status, &vehicle.Archived, &vehicle.ReservedFrom, &vehicle.ReservedUntil,
			&category.DefaultMonthlyCost, &category.DefaultYearlyCost, &rateChanges,
		); err != nil {
			return fmt.Errorf("scan forecast vehicle: %w", err)
		}
		if len(rateChanges) > 0 {
			if err := json.Unmarshal(rateChanges, &vehicle.RateChanges); err != nil {
				return fmt.Errorf("forecast rate changes: %w", err)
			}
		}
		category.ID = vehicle.CategoryID
		data.Vehicles = append(data.Vehicles, vehicle)
		data.Categories[category.ID] = category
	}
	return rows.Err()
}

func loadForecastAgreements(ctx context.Context, q dbQuerier, data *dashboardForecastData) error {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.person_id, a.amount, a.period, a.start_date, a.end_date,
		       COALESCE(array_agg(v.vehicle_id ORDER BY v.vehicle_id)
		         FILTER (WHERE v.vehicle_id IS NOT NULL), '{}')
		  FROM flat_rate_periods a LEFT JOIN flat_rate_period_vehicles v ON v.period_id=a.id
		 GROUP BY a.id ORDER BY a.id`)
	if err != nil {
		return fmt.Errorf("load forecast agreements: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var agreement models.FlatRatePeriod
		if err := rows.Scan(
			&agreement.ID, &agreement.PersonID, &agreement.Amount, &agreement.Period,
			&agreement.StartDate, &agreement.EndDate, &agreement.VehicleIDs,
		); err != nil {
			return fmt.Errorf("scan forecast agreement: %w", err)
		}
		data.Agreements[agreement.PersonID] = append(data.Agreements[agreement.PersonID], agreement)
	}
	return rows.Err()
}

func loadForecastRecurring(ctx context.Context, q dbQuerier, data *dashboardForecastData) error {
	rows, err := q.Query(ctx, `
		SELECT amount, period, start_date, end_date FROM recurring_charges ORDER BY id`)
	if err != nil {
		return fmt.Errorf("load forecast recurring charges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var period models.FlatRatePeriod
		if err := rows.Scan(&period.Amount, &period.Period, &period.StartDate, &period.EndDate); err != nil {
			return fmt.Errorf("scan forecast recurring charge: %w", err)
		}
		data.Recurring = append(data.Recurring, period)
	}
	return rows.Err()
}

type forecastSources struct {
	Actual       []models.FlatRatePeriod
	Future       []models.FlatRatePeriod
	Reservations []models.FlatRatePeriod
	Charges      []dashboardForecastCharge
}

func buildForecastSources(data dashboardForecastData, now time.Time) (forecastSources, []string) {
	sources := forecastSources{
		Actual: []models.FlatRatePeriod{}, Future: []models.FlatRatePeriod{},
		Reservations: []models.FlatRatePeriod{}, Charges: data.Charges,
	}
	warnings := []string{}
	var openEnded, legacyPrice, ambiguousReservation, closedFuture, linkedOutside bool
	until := models.DayAfter(now)
	personIDs := make([]int64, 0, len(data.Agreements))
	for id := range data.Agreements {
		personIDs = append(personIDs, id)
	}
	slices.Sort(personIDs)
	for _, id := range personIDs {
		for _, agreement := range data.Agreements[id] {
			sources.Actual = append(sources.Actual, agreement)
			sources.Future = append(sources.Future, agreement)
			openEnded = openEnded || agreement.EndDate == nil
		}
	}
	for _, vehicle := range data.Vehicles {
		if vehicle.Status == models.StatusReserved {
			fromMismatch := vehicle.ReservedFrom != nil && !vehicle.ReservedFrom.Equal(vehicle.StartDate)
			toMismatch := vehicle.ReservedUntil != nil &&
				(vehicle.EndDate == nil || !vehicle.ReservedUntil.Equal(*vehicle.EndDate))
			ambiguousReservation = ambiguousReservation || fromMismatch || toMismatch
		}
		covering := coveringAgreements(data.Agreements[vehicle.PersonID], vehicle.ID, vehicle.StartDate)
		if len(covering) != 0 {
			// Preserve personRent's ownership rule, not a time-overlap subtraction.
			for _, agreement := range covering {
				if agreement.EndDate != nil && (vehicle.EndDate == nil || vehicle.EndDate.After(*agreement.EndDate)) {
					linkedOutside = true
				}
			}
			continue
		}
		// Mit Preisänderungen (Ausbau 29): die Prognose rechnet künftige Monate mit dem
		// dann gültigen Satz.
		period := vehicle.AsPeriod(data.Categories[vehicle.CategoryID])
		sources.Actual = append(sources.Actual, period)
		legacyPrice = legacyPrice || (vehicle.Rate == 0 && vehicle.CostOverride == nil)
		closed := vehicle.Archived || vehicle.Status == models.StatusCancelled || vehicle.Status == models.StatusCollected
		if closed {
			closedFuture = closedFuture || vehicle.EndDate == nil || !vehicle.EndDate.Before(until)
			continue
		}
		switch vehicle.Status {
		case models.StatusStored:
			sources.Future = append(sources.Future, period)
		case models.StatusReserved:
			sources.Reservations = append(sources.Reservations, period)
		}
		openEnded = openEnded || vehicle.EndDate == nil
	}
	sources.Actual = append(sources.Actual, data.Recurring...)
	sources.Future = append(sources.Future, data.Recurring...)
	for _, period := range data.Recurring {
		openEnded = openEnded || period.EndDate == nil
	}
	if openEnded {
		warnings = append(warnings, "Offene Miet-, Pauschal- oder Nebenkostenzeiträume werden bis zum Ende des Planjahres unverändert fortgeführt.")
	}
	if legacyPrice {
		warnings = append(warnings, "Alte Buchungen ohne gespeicherten Einzelpreis verwenden wie die Abrechnung den heutigen Tarif als Rückfallwert.")
	}
	if ambiguousReservation {
		warnings = append(warnings, "Reservierungsdaten weichen vom Mietzeitraum ab. Die Prognose verwendet Mietbeginn und Mietende; bitte prüfen.")
	}
	if closedFuture {
		warnings = append(warnings, "Geschlossene oder archivierte Fahrzeuge mit offenem oder zukünftigem Enddatum sind aus der Zukunftsprognose ausgeschlossen.")
	}
	if linkedOutside {
		warnings = append(warnings, "Pauschalgebundene Fahrzeuge starten nach Pauschalende keine automatische Einzelmiete; abweichende Laufzeiten bitte prüfen.")
	}
	return sources, warnings
}

func forecastRangeCents(periods []models.FlatRatePeriod, charges []dashboardForecastCharge, from, to time.Time) int64 {
	if !to.After(from) {
		return 0
	}
	var total int64
	for _, period := range periods {
		total += int64(math.Round(period.CostInRange(from, to) * 100))
	}
	for _, charge := range charges {
		if !charge.On.Before(from) && charge.On.Before(to) {
			total += int64(math.Round(charge.Amount * 100))
		}
	}
	return total
}

func forecastMonths(
	periods []models.FlatRatePeriod,
	charges []dashboardForecastCharge,
	year int,
	from, to time.Time,
) []float64 {
	months := make([]float64, 12)
	startOfYear := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	endOfYear := startOfYear.AddDate(1, 0, 0)
	if from.Before(startOfYear) {
		from = startOfYear
	}
	if to.After(endOfYear) {
		to = endOfYear
	}
	if !to.After(from) {
		return months
	}
	// Each source is rounded by the accrual model before summation. Differences
	// between cumulative boundaries keep its remainder in its own active month,
	// rather than rounding both sides of a cutoff and spreading the excess over
	// unrelated contracts. Positive sources stay nonnegative; real credits retain
	// their sign and date. The month deltas telescope to the exact annual remainder.
	previous := forecastRangeCents(periods, charges, startOfYear, from)
	for month := range 12 {
		end := startOfYear.AddDate(0, month+1, 0)
		if !end.After(from) {
			continue
		}
		if end.After(to) {
			end = to
		}
		current := forecastRangeCents(periods, charges, startOfYear, end)
		months[month] = float64(current-previous) / 100
		previous = current
		if !end.Before(to) {
			break
		}
	}
	return months
}

func sumForecastMonths(months []float64) float64 {
	var total int64
	for _, amount := range months {
		total += int64(math.Round(amount * 100))
	}
	return float64(total) / 100
}

func forecastHasSpan(periods []models.FlatRatePeriod, charges []dashboardForecastCharge, from, to time.Time) bool {
	if !to.After(from) {
		return false
	}
	for _, period := range periods {
		end := to
		if period.EndDate != nil && models.DayAfter(*period.EndDate).Before(end) {
			end = models.DayAfter(*period.EndDate)
		}
		if end.After(from) && end.After(period.StartDate) {
			return true
		}
	}
	for _, charge := range charges {
		if !charge.On.Before(from) && charge.On.Before(to) {
			return true
		}
	}
	return false
}

func buildDashboardForecast(
	data dashboardForecastData,
	ys dashboardYears,
	requested int,
	now time.Time,
) dashboardForecastResponse {
	currentYear := now.Year()
	year := ys.canonical(requested, currentYear, true)
	sources, warnings := buildForecastSources(data, now)
	rep := dashboardForecastResponse{
		Year: year, CurrentYear: currentYear, AsOf: now.Format(dateLayout),
		AvailableYears: slices.Clone(ys.Available), ForecastYears: slices.Clone(ys.Available),
		ActualByMonth: make([]float64, 12), ForecastByMonth: make([]float64, 12),
		ReservationsByMonth: make([]float64, 12), Years: []dashboardForecastYear{}, Warnings: warnings,
		Assumptions: []string{
			"Unverbindliche Bestandsprognose der zeitanteiligen Mieterlöse und bekannten Nebenkosten; keine Prognose von Zahlungseingängen oder Steuerwerten.",
			"Ist bis einschließlich Stichtag entspricht der heutigen Datenlage, nicht einem unveränderlichen historischen Abschluss.",
			"Ab morgen werden bestehende Mietpreise und Laufzeiten fortgeführt; keine neuen Buchungen, Preissteigerungen oder saisonalen Zusatzerlöse erfunden.",
			"Reservierungen sind ein gesondertes optionales Szenario ohne Eintrittswahrscheinlichkeit; pauschalgebundene Fahrzeuge zählen nicht zusätzlich.",
			"Pauschalen und wiederkehrende Nebenkosten gelten nach ihren eigenen Zeiträumen. Erfasste Einmalkosten zählen einmal am Leistungsdatum; Strombedarf allein erzeugt keinen Preis.",
			"Ein Vorjahresvergleich verwendet ausschließlich das unmittelbar vorherige Kalenderjahr; Datenlücken werden nicht durch andere Jahre ersetzt.",
		},
	}
	if currentYear < 2100 {
		rep.ForecastYears = append(rep.ForecastYears, currentYear+1)
	}
	until := models.DayAfter(now)
	for _, candidate := range rep.ForecastYears {
		start := time.Date(candidate, 1, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(1, 0, 0)
		actualEnd := end
		if actualEnd.After(until) {
			actualEnd = until
		}
		forecastStart := start
		if forecastStart.Before(until) {
			forecastStart = until
		}
		actual := forecastMonths(sources.Actual, sources.Charges, candidate, start, actualEnd)
		forecast := forecastMonths(sources.Future, sources.Charges, candidate, forecastStart, end)
		reservations := forecastMonths(sources.Reservations, nil, candidate, forecastStart, end)
		hasData := ys.Present[candidate] || forecastHasSpan(sources.Future, sources.Charges, forecastStart, end) ||
			forecastHasSpan(sources.Reservations, nil, forecastStart, end)
		rep.Years = append(rep.Years, dashboardForecastYear{
			Year: candidate, Actual: sumForecastMonths(actual), Forecast: sumForecastMonths(forecast),
			Reservations: sumForecastMonths(reservations), HasData: hasData,
		})
		if candidate == year {
			rep.HasYearData = hasData
			rep.ActualByMonth, rep.ForecastByMonth, rep.ReservationsByMonth = actual, forecast, reservations
		}
		if candidate == year-1 && candidate < currentYear && ys.Present[candidate] {
			rep.PreviousByMonth = actual
		}
	}
	if year > currentYear {
		rep.Assumptions = append(rep.Assumptions,
			"Vorjahresvergleich im Planjahr erst nach Kalenderjahresende des Vergleichsjahres verfügbar; kein steuerlicher Jahresabschluss erforderlich.")
	}
	return rep
}
