//go:build integration

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests intentionally do not run in parallel: both dashboard endpoints
// aggregate the whole database. testHandler only uses PARKRR_TEST_DATABASE_URL.
// Old, fixed business dates keep unrelated present-day fixtures out of the
// assertion window; teardown removes only this fixture's IDs.
type forecastDBFixture struct {
	h        *Handler
	category int64
	persons  []int64
}

func newForecastDBFixture(t *testing.T, day string) *forecastDBFixture {
	t.Helper()
	h := testHandler(t)
	now := forecastDate(day).Add(12 * time.Hour)
	h.Now = func() time.Time { return now }
	f := &forecastDBFixture{h: h}
	if err := h.Pool.QueryRow(t.Context(), `INSERT INTO categories
		(name, default_monthly_cost, default_yearly_cost) VALUES ($1,62,365) RETURNING id`,
		fmt.Sprintf("Forecast-Integration-%d", time.Now().UnixNano())).Scan(&f.category); err != nil {
		t.Fatalf("create forecast category: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := purgeExec(ctx, h.Pool, `DELETE FROM persons WHERE id=ANY($1::bigint[])`, f.persons); err != nil {
			t.Errorf("cleanup forecast persons: %v", err)
		}
		if _, err := h.Pool.Exec(ctx, `DELETE FROM categories WHERE id=$1`, f.category); err != nil {
			t.Errorf("cleanup forecast category: %v", err)
		}
	})
	f.person(t)
	return f
}

func (f *forecastDBFixture) person(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := f.h.Pool.QueryRow(t.Context(), `INSERT INTO persons
		(first_name,last_name) VALUES ('Forecast','Integration') RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create forecast person: %v", err)
	}
	f.persons = append(f.persons, id)
	return id
}

func (f *forecastDBFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.h.Pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatalf("seed forecast fixture: %v", err)
	}
}

type forecastDBVehicle struct {
	person                           int64
	start, period, status            string
	end, reservedFrom, reservedUntil *time.Time
	rate                             float64
	override                         *float64
	archived, paid, needsPower       bool
}

func (f *forecastDBFixture) vehicle(t *testing.T, v forecastDBVehicle) int64 {
	t.Helper()
	if v.person == 0 {
		v.person = f.persons[0]
	}
	if v.period == "" {
		v.period = "monthly"
	}
	if v.status == "" {
		v.status = "stored"
	}
	var id int64
	err := f.h.Pool.QueryRow(t.Context(), `INSERT INTO vehicles
		(person_id,category_id,billing_period,rate,cost_override,start_date,end_date,
		 status,archived,paid,needs_power,reserved_from,reserved_until)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
		v.person, f.category, v.period, v.rate, v.override, v.start, v.end,
		v.status, v.archived, v.paid, v.needsPower, v.reservedFrom, v.reservedUntil).Scan(&id)
	if err != nil {
		t.Fatalf("create forecast vehicle: %v", err)
	}
	return id
}

func forecastDBGet(t *testing.T, h *Handler, year string) dashboardForecastResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	h.OverviewForecast(rec, httptest.NewRequest(http.MethodGet, "/api/overview/forecast?year="+year, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("forecast %s: status %d: %s", year, rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("forecast must not be cached: %v", rec.Header())
	}
	var rep dashboardForecastResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode forecast: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decode forecast fields: %v", err)
	}
	if _, exists := fields["previous_by_month"]; !exists {
		t.Error("missing prior history must be explicit JSON null, not an omitted field")
	}
	for name, amounts := range map[string][]float64{
		"actual": rep.ActualByMonth, "forecast": rep.ForecastByMonth, "reservations": rep.ReservationsByMonth,
	} {
		if len(amounts) != 12 {
			t.Fatalf("%s months=%d, want 12", name, len(amounts))
		}
		for month, amount := range amounts {
			if math.IsInf(amount, 0) || math.IsNaN(amount) || math.Abs(amount*100-math.Round(amount*100)) > 1e-7 {
				t.Errorf("%s month %d is not a finite cent amount: %v", name, month+1, amount)
			}
		}
	}
	for _, row := range rep.Years {
		if row.Year == rep.Year {
			assertForecastDBCents(t, "annual actual/months", row.Actual, forecastDBSum(rep.ActualByMonth))
			assertForecastDBCents(t, "annual forecast/months", row.Forecast, forecastDBSum(rep.ForecastByMonth))
			assertForecastDBCents(t, "annual reservations/months", row.Reservations, forecastDBSum(rep.ReservationsByMonth))
			return rep
		}
	}
	t.Fatalf("selected year %d missing from annual rows", rep.Year)
	return rep
}

func forecastDBOverview(t *testing.T, h *Handler, year string) overviewResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Overview(rec, httptest.NewRequest(http.MethodGet, "/api/overview?year="+year, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("overview %s: status %d: %s", year, rec.Code, rec.Body.String())
	}
	var rep overviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	return rep
}

func forecastDBSum(amounts []float64) float64 {
	var cents int64
	for _, amount := range amounts {
		cents += int64(math.Round(amount * 100))
	}
	return float64(cents) / 100
}

func assertForecastDBCents(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Round(got*100) != math.Round(want*100) {
		t.Errorf("%s = %.2f EUR, want %.2f EUR", label, got, want)
	}
}

func TestDashboardForecastIntegrationBusinessYearsAndCanonicalization(t *testing.T) {
	f := newForecastDBFixture(t, "2010-01-15")
	h := f.h
	empty := forecastDBGet(t, h, "2010")
	if empty.HasYearData || empty.PreviousByMonth != nil {
		t.Fatal("test requires an isolated database without pre-existing 2009/2010 business data")
	}
	zero := 0.0
	f.vehicle(t, forecastDBVehicle{
		start: "2000-01-01", end: forecastDatePtr("2002-12-31"), override: &zero,
		status: "collected", archived: true,
	})
	f.exec(t, `INSERT INTO flat_rate_periods (person_id,amount,period,start_date,end_date)
		VALUES ($1,0,'yearly','2004-01-01','2004-12-31')`, f.persons[0])
	f.exec(t, `INSERT INTO recurring_charges (person_id,description,amount,period,start_date,end_date)
		VALUES ($1,'zero recurring',0,'monthly','2005-01-01','2005-01-31')`, f.persons[0])
	f.exec(t, `INSERT INTO charges (person_id,description,amount,charged_on)
		VALUES ($1,'zero dated charge',0,'2006-01-01')`, f.persons[0])
	f.exec(t, `INSERT INTO payments (person_id,amount,paid_on,reversed)
		VALUES ($1,1,'2007-01-01',true)`, f.persons[0])
	f.vehicle(t, forecastDBVehicle{start: "2012-01-01", rate: 31})

	rep := forecastDBGet(t, h, "2010")
	wantYears := []int{2000, 2001, 2002, 2004, 2005, 2006, 2007, 2010}
	if !slices.Equal(rep.AvailableYears, wantYears) {
		t.Errorf("business years=%v, want %v (zero, archived, multi-year and reversed history count)", rep.AvailableYears, wantYears)
	}
	if !slices.Equal(rep.ForecastYears, append(slices.Clone(wantYears), 2011)) || rep.HasYearData {
		t.Errorf("empty current/plan years: %+v", rep)
	}
	for _, year := range wantYears[:len(wantYears)-1] {
		t.Run(fmt.Sprintf("history_%d", year), func(t *testing.T) {
			selected := forecastDBGet(t, h, fmt.Sprint(year))
			ov := forecastDBOverview(t, h, fmt.Sprint(year))
			if selected.Year != year || !selected.HasYearData || ov.Year != year || !ov.HasYearData {
				t.Errorf("zero-valued history was discarded: forecast=%+v overview=%+v", selected, ov)
			}
			assertForecastDBCents(t, "zero history actual", forecastDBSum(selected.ActualByMonth), 0)
			assertForecastDBCents(t, "history has no future", forecastDBSum(selected.ForecastByMonth), 0)
			assertForecastDBCents(t, "history has no reservation remainder", forecastDBSum(selected.ReservationsByMonth), 0)
			if year == 2002 && (selected.PreviousByMonth == nil || !ov.PreviousYearHasData) {
				t.Error("real zero-valued 2001 must be an available comparison, not null")
			}
			if year == 2004 && (selected.PreviousByMonth != nil || ov.PreviousYearHasData) {
				t.Error("missing 2003 must stay null; do not substitute available 2002")
			}
		})
	}
	for _, requested := range []string{"2003", "2008", "2009", "2012", "1999", "2101", "invalid", "99999999999999999999"} {
		t.Run("fallback_"+requested, func(t *testing.T) {
			got := forecastDBGet(t, h, requested)
			ov := forecastDBOverview(t, h, requested)
			if got.Year != 2010 || ov.Year != 2010 || got.CurrentYear != 2010 || ov.CurrentYear != 2010 {
				t.Errorf("request %q did not canonicalize to current year: forecast=%d overview=%d", requested, got.Year, ov.Year)
			}
		})
	}
	plan := forecastDBGet(t, h, "2011")
	if plan.Year != 2011 || plan.HasYearData || plan.PreviousByMonth != nil {
		t.Errorf("empty next year or unfinished previous comparison: %+v", plan)
	}
	if ov := forecastDBOverview(t, h, "2011"); ov.Year != 2010 {
		t.Error("overview must not navigate into a forecast-only year")
	}
}

func TestDashboardForecastIntegrationAccrualOwnershipAndReadOnly(t *testing.T) {
	f := newForecastDBFixture(t, "2010-01-15")
	h, pid := f.h, f.persons[0]
	baseline := forecastDBGet(t, h, "2010")
	baselineOverview := forecastDBOverview(t, h, "2010")
	if baseline.HasYearData || baseline.PreviousByMonth != nil {
		t.Fatal("test requires an isolated database without pre-existing 2009/2010 business data")
	}
	janEnd, yearEnd := forecastDatePtr("2010-01-31"), forecastDatePtr("2010-12-31")
	zero, legacy, ignoredOverride := 0.0, 31.0, 999.0
	f.vehicle(t, forecastDBVehicle{start: "2010-01-01", end: janEnd, rate: 31, override: &ignoredOverride, paid: true})
	f.vehicle(t, forecastDBVehicle{start: "2010-01-01", needsPower: true}) // Legacy category fallback; open end.
	f.vehicle(t, forecastDBVehicle{start: "2010-01-01", end: yearEnd, override: &zero})
	f.vehicle(t, forecastDBVehicle{start: "2010-01-01", end: janEnd, override: &legacy})
	f.vehicle(t, forecastDBVehicle{start: "2010-01-01", end: yearEnd, period: "yearly"})
	f.vehicle(t, forecastDBVehicle{start: "2010-01-01", rate: 31, archived: true})
	f.vehicle(t, forecastDBVehicle{start: "2010-01-01", rate: 62, status: "collected"})
	f.vehicle(t, forecastDBVehicle{start: "2010-01-01", rate: 93, status: "cancelled"})
	f.vehicle(t, forecastDBVehicle{
		start: "2010-01-01", end: janEnd, rate: 62, status: "reserved",
		reservedFrom: forecastDatePtr("2010-01-02"), reservedUntil: janEnd,
	})
	f.vehicle(t, forecastDBVehicle{
		start: "2010-02-01", end: forecastDatePtr("2010-02-28"), rate: 93, status: "reserved",
	})
	// This settled, expired vehicle would be eligible for automatic archival.
	// Neither GET may perform that mutation; the full-row snapshot checks it.
	f.vehicle(t, forecastDBVehicle{
		start: "2009-01-01", end: forecastDatePtr("2009-12-31"), override: &zero, paid: true,
	})

	linkedPerson := f.person(t)
	linkedVehicle := f.vehicle(t, forecastDBVehicle{person: linkedPerson, start: "2010-01-01", end: yearEnd, rate: 1000})
	linkedReservation := f.vehicle(t, forecastDBVehicle{
		person: linkedPerson, start: "2010-01-01", end: yearEnd, rate: 2000, status: "reserved",
	})
	f.vehicle(t, forecastDBVehicle{person: linkedPerson, start: "2010-01-01", end: janEnd, rate: 31})
	var agreementID int64
	if err := h.Pool.QueryRow(t.Context(), `INSERT INTO flat_rate_periods
		(person_id,amount,period,start_date,end_date) VALUES ($1,124,'monthly','2010-01-01','2010-01-31') RETURNING id`,
		linkedPerson).Scan(&agreementID); err != nil {
		t.Fatalf("create linked agreement: %v", err)
	}
	f.exec(t, `INSERT INTO flat_rate_period_vehicles (period_id,vehicle_id) VALUES ($1,$2),($1,$3)`,
		agreementID, linkedVehicle, linkedReservation)
	f.exec(t, `INSERT INTO flat_rate_period_payments (period_id,period_key,amount) VALUES ($1,'2010-01',60)`, agreementID)
	f.exec(t, `INSERT INTO recurring_charges
		(person_id,vehicle_id,description,amount,period,start_date,end_date,paid,paid_periods,paid_fixed)
		VALUES ($1,$2,'bound extra',31,'monthly','2010-01-01','2010-01-31',true,ARRAY['2010-01'],'{"2010-01":5}')`,
		linkedPerson, linkedVehicle)
	personWide := f.person(t)
	f.vehicle(t, forecastDBVehicle{person: personWide, start: "2010-01-01", end: yearEnd, rate: 3000})
	f.exec(t, `INSERT INTO flat_rate_periods (person_id,amount,period,start_date,end_date,paid)
		VALUES ($1,62,'monthly','2010-01-01','2010-01-31',true)`, personWide)
	f.exec(t, `INSERT INTO charges (person_id,vehicle_id,description,amount,quantity,charged_on,paid)
		VALUES ($1,$2,'already accrued extra',10.25,2,'2010-01-15',true),
		       ($1,$2,'future extra',3.25,2,'2010-01-16',false),
		       ($1,NULL,'plan extra',5,1,'2011-02-01',false)`, linkedPerson, linkedVehicle)
	var paymentID int64
	if err := h.Pool.QueryRow(t.Context(), `INSERT INTO payments (person_id,amount,paid_on)
		VALUES ($1,999,'2010-01-15') RETURNING id`, pid).Scan(&paymentID); err != nil {
		t.Fatalf("create actual receipt: %v", err)
	}
	f.exec(t, `INSERT INTO payments (person_id,amount,paid_on,reversed) VALUES ($1,111,'2010-01-15',true)`, pid)
	f.exec(t, `INSERT INTO payment_tax_metadata (payment_id,received_on,tax_property_id)
		SELECT $1,'2010-01-14',id FROM tax_properties WHERE is_default`, paymentID)

	before := f.financialSnapshot(t)
	current := forecastDBGet(t, h, "2010")
	ov := forecastDBOverview(t, h, "2010")
	assertForecastDBCents(t, "current actual incl extras, not cash", forecastDBSum(current.ActualByMonth), 335.50)
	assertForecastDBCents(t, "current remaining rent and known extras", forecastDBSum(current.ForecastByMonth), 1230.50)
	assertForecastDBCents(t, "January remaining", current.ForecastByMonth[0], 214.50)
	assertForecastDBCents(t, "optional reservation remainder", forecastDBSum(current.ReservationsByMonth), 125)
	assertForecastDBCents(t, "January optional reservation", current.ReservationsByMonth[0], 32)
	assertForecastDBCents(t, "February optional reservation", current.ReservationsByMonth[1], 93)
	assertForecastDBCents(t, "overview current accrual parity", forecastDBSum(current.ActualByMonth), ov.AccruedThisYear)
	assertForecastDBCents(t, "actual receipts exclude reversal", ov.PaymentsThisYear-baselineOverview.PaymentsThisYear, 999)
	if !current.HasYearData || !ov.HasYearData || !ov.PreviousYearHasData || current.PreviousByMonth == nil {
		t.Error("zero prior history and nonzero current history must remain distinguishable from missing history")
	}
	if current.AsOf != "2010-01-15" || current.Year != 2010 || current.CurrentYear != 2010 {
		t.Errorf("business cutoff metadata: %+v", current)
	}
	for _, fragment := range []string{"Offene", "Rückfallwert", "Reservierungsdaten", "archivierte", "Pauschalende"} {
		if !strings.Contains(strings.Join(current.Warnings, " "), fragment) {
			t.Errorf("missing warning %q: %v", fragment, current.Warnings)
		}
	}
	history := forecastDBGet(t, h, "2009")
	historyOverview := forecastDBOverview(t, h, "2009")
	assertForecastDBCents(t, "prior full-year overview parity", forecastDBSum(history.ActualByMonth), historyOverview.AccruedThisYear)
	assertForecastDBCents(t, "zero prior year", forecastDBSum(history.ActualByMonth), 0)
	if history.PreviousByMonth != nil {
		t.Error("missing 2008 must not be substituted")
	}
	plan := forecastDBGet(t, h, "2011")
	assertForecastDBCents(t, "next year is not actual", forecastDBSum(plan.ActualByMonth), 0)
	assertForecastDBCents(t, "next year only open standalone plus dated extra", forecastDBSum(plan.ForecastByMonth), 749)
	assertForecastDBCents(t, "closed reservations do not recur next year", forecastDBSum(plan.ReservationsByMonth), 0)
	if plan.PreviousByMonth != nil || !strings.Contains(strings.Join(plan.Assumptions, " "), "Kalenderjahresende") {
		t.Error("plan-year comparison must not show an unfinished current year as full prior-year actual")
	}
	// A repeated read also exercises the overview cache without relaxing the
	// read-only contract for payment flags, tax metadata or expired vehicles.
	if again := forecastDBGet(t, h, "2010"); !reflect.DeepEqual(current, again) {
		t.Error("identical database and cutoff produced a different forecast")
	}
	if again := forecastDBOverview(t, h, "2010"); !reflect.DeepEqual(ov, again) {
		t.Error("overview values changed after historical/plan-year forecast reads")
	}
	if after := f.financialSnapshot(t); before != after {
		t.Errorf("read-only dashboard calls changed financial or archival state:\nbefore=%s\nafter=%s", before, after)
	}
}

func (f *forecastDBFixture) financialSnapshot(t *testing.T) string {
	t.Helper()
	var snapshot string
	err := f.h.Pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'persons',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM persons p WHERE id=ANY($1::bigint[])),
		'vehicles',(SELECT jsonb_agg(to_jsonb(v) ORDER BY id) FROM vehicles v WHERE person_id=ANY($1::bigint[])),
		'agreements',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM flat_rate_periods a WHERE person_id=ANY($1::bigint[])),
		'links',(SELECT jsonb_agg(to_jsonb(l) ORDER BY period_id,vehicle_id) FROM flat_rate_period_vehicles l
			WHERE period_id IN (SELECT id FROM flat_rate_periods WHERE person_id=ANY($1::bigint[]))),
		'period_payments',(SELECT jsonb_agg(to_jsonb(p) ORDER BY period_id,period_key) FROM flat_rate_period_payments p
			WHERE period_id IN (SELECT id FROM flat_rate_periods WHERE person_id=ANY($1::bigint[]))),
		'recurring',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM recurring_charges r WHERE person_id=ANY($1::bigint[])),
		'charges',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM charges c WHERE person_id=ANY($1::bigint[])),
		'payments',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM payments p WHERE person_id=ANY($1::bigint[])),
		'tax_metadata',(SELECT jsonb_agg(to_jsonb(m) ORDER BY payment_id) FROM payment_tax_metadata m
			WHERE payment_id IN (SELECT id FROM payments WHERE person_id=ANY($1::bigint[]))),
		'invoices',(SELECT jsonb_agg(to_jsonb(i) ORDER BY id) FROM invoices i WHERE person_id=ANY($1::bigint[])),
		'status_history',(SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM vehicle_status_history s
			WHERE vehicle_id IN (SELECT id FROM vehicles WHERE person_id=ANY($1::bigint[]))),
		'category',(SELECT to_jsonb(c) FROM categories c WHERE id=$2)
	)::text`, f.persons, f.category).Scan(&snapshot)
	if err != nil {
		t.Fatalf("snapshot financial fixture: %v", err)
	}
	return snapshot
}

func TestDashboardForecastIntegrationRoundingNeverInventsNegativeRent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		juneAmount  float64
		marchCharge float64
	}{
		{name: "tiny later contract", juneAmount: .01},
		{name: "normal later contract", juneAmount: 30},
		{name: "real dated credit", juneAmount: .01, marchCharge: -7.50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newForecastDBFixture(t, "2010-02-14")
			if baseline := forecastDBGet(t, f.h, "2010"); baseline.HasYearData {
				t.Fatal("test requires an isolated database without pre-existing 2010 business data")
			}
			// Half a cent already rounds to one accrued cent per contract. Its
			// zero remainder must not reduce an unrelated June contract or credit.
			f.exec(t, `INSERT INTO vehicles
				(person_id,category_id,rate,start_date,end_date,status,billing_period)
				SELECT $1,$2,0.01,'2010-02-01'::date,'2010-02-28'::date,'stored','monthly'
				FROM generate_series(1,100)`, f.persons[0], f.category)
			f.vehicle(t, forecastDBVehicle{
				start: "2010-06-01", end: forecastDatePtr("2010-06-30"), rate: tc.juneAmount,
			})
			if tc.marchCharge != 0 {
				f.exec(t, `INSERT INTO charges (person_id,description,amount,charged_on)
					VALUES ($1,'forecast credit',$2,'2010-03-01')`, f.persons[0], tc.marchCharge)
			}
			before := f.financialSnapshot(t)
			rep := forecastDBGet(t, f.h, "2010")
			assertForecastDBCents(t, "actual tiny contracts", forecastDBSum(rep.ActualByMonth), 1)
			assertForecastDBCents(t, "future contract and credit total",
				forecastDBSum(rep.ForecastByMonth), tc.juneAmount+tc.marchCharge)
			for month, amount := range rep.ForecastByMonth {
				want := 0.0
				switch month {
				case 2:
					want = tc.marchCharge
				case 5:
					want = tc.juneAmount
				}
				assertForecastDBCents(t, fmt.Sprintf("future month %d", month+1), amount, want)
			}
			ov := forecastDBOverview(t, f.h, "2010")
			assertForecastDBCents(t, "tiny-contract overview parity", forecastDBSum(rep.ActualByMonth), ov.AccruedThisYear)
			if after := f.financialSnapshot(t); after != before {
				t.Error("forecast rounding changed persisted financial state")
			}
		})
	}
}

func TestDashboardForecastIntegrationPausesMatchBilling(t *testing.T) {
	f := newForecastDBFixture(t, "2010-01-15")
	janEnd := forecastDatePtr("2010-01-31")
	ended := f.vehicle(t, forecastDBVehicle{start: "2010-01-01", end: janEnd, rate: 31})
	open := f.vehicle(t, forecastDBVehicle{start: "2010-01-01", end: janEnd, rate: 62})
	f.exec(t, `INSERT INTO vehicle_pauses (vehicle_id,start_date,until,ended) VALUES
		($1,'2010-01-05','2010-01-09',true),($2,'2010-01-11','2010-01-12',false)`, ended, open)
	f.exec(t, `INSERT INTO recurring_charges (person_id,vehicle_id,description,amount,period,start_date,end_date)
		VALUES ($1,$2,'bound paused extra',31,'monthly','2010-01-01','2010-01-31'),
		       ($1,NULL,'person extra continues',31,'monthly','2010-01-01','2010-01-31')`, f.persons[0], open)
	// The vehicle's pause is recorded under its Pauschale but must not reduce
	// that person's agreed flat amount, exactly as in personRent.
	flatPerson := f.person(t)
	flatVehicle := f.vehicle(t, forecastDBVehicle{person: flatPerson, start: "2010-01-01", end: janEnd, rate: 9000})
	f.exec(t, `INSERT INTO vehicle_pauses (vehicle_id,start_date,until,ended)
		VALUES ($1,'2010-01-01','2010-01-31',true)`, flatVehicle)
	f.exec(t, `INSERT INTO flat_rate_periods (person_id,amount,period,start_date,end_date)
		VALUES ($1,31,'monthly','2010-01-01','2010-01-31')`, flatPerson)
	before := f.financialSnapshot(t)
	rep := forecastDBGet(t, f.h, "2010")
	// 10 ended-pause rent + 20 open-pause rent + 10 paused bound extra
	// + 15 person extra + 15 Pauschale, all at whole euros per active day.
	assertForecastDBCents(t, "actual respects ended and open pauses", forecastDBSum(rep.ActualByMonth), 70)
	assertForecastDBCents(t, "future keeps open pause despite expired planned return", forecastDBSum(rep.ForecastByMonth), 48)
	ov := forecastDBOverview(t, f.h, "2010")
	assertForecastDBCents(t, "paused billing/dashboard actual parity", forecastDBSum(rep.ActualByMonth), ov.AccruedThisYear)
	if after := f.financialSnapshot(t); after != before {
		t.Error("forecast reads changed persisted financial values")
	}
}

type forecastSnapshotDB struct {
	pool       *pgxpool.Pool
	afterYears func() error
	options    pgx.TxOptions
	readOnly   string
	isolation  string
}

func (db *forecastSnapshotDB) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	db.options = options
	tx, err := db.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `SHOW transaction_read_only`).Scan(&db.readOnly); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	if err := tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&db.isolation); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, err
	}
	return &forecastSnapshotTx{Tx: tx, afterYears: db.afterYears}, nil
}

type forecastSnapshotTx struct {
	pgx.Tx
	afterYears func() error
	reads      int
}

func (tx *forecastSnapshotTx) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	tx.reads++
	if tx.reads == 2 && tx.afterYears != nil {
		if err := tx.afterYears(); err != nil {
			return nil, err
		}
	}
	return tx.Tx.Query(ctx, query, args...)
}

func TestDashboardForecastIntegrationRepeatableReadSnapshot(t *testing.T) {
	f := newForecastDBFixture(t, "2010-01-15")
	if f.h.Pool.Config().MaxConns < 2 {
		t.Skip("snapshot interleaving requires two connections to the isolated test database")
	}
	before := forecastDBGet(t, f.h, "2010")
	f.exec(t, `INSERT INTO charges (person_id,description,amount,charged_on)
		VALUES ($1,'first snapshot charge',10,'2010-01-01')`, f.persons[0])
	db := &forecastSnapshotDB{pool: f.h.Pool}
	db.afterYears = func() error {
		_, err := f.h.Pool.Exec(t.Context(), `INSERT INTO charges (person_id,description,amount,charged_on)
			VALUES ($1,'concurrent snapshot charge',20,'2010-01-01')`, f.persons[0])
		return err
	}
	rep, err := readDashboardForecast(t.Context(), db, 2010, f.h.now())
	if err != nil {
		t.Fatalf("read repeatable snapshot: %v", err)
	}
	if db.options.AccessMode != pgx.ReadOnly || db.options.IsoLevel != pgx.RepeatableRead ||
		db.readOnly != "on" || db.isolation != "repeatable read" {
		t.Errorf("forecast transaction is not a real read-only repeatable-read snapshot: %+v / %s / %s",
			db.options, db.readOnly, db.isolation)
	}
	assertForecastDBCents(t, "in-flight snapshot excludes later commit",
		forecastDBSum(rep.ActualByMonth)-forecastDBSum(before.ActualByMonth), 10)
	after := forecastDBGet(t, f.h, "2010")
	assertForecastDBCents(t, "new snapshot includes later commit",
		forecastDBSum(after.ActualByMonth)-forecastDBSum(before.ActualByMonth), 30)
}
