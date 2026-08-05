package models

import (
	"math"
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func tp(t time.Time) *time.Time { return &t }

// exact compares to within a cent: with calendar-accurate proration a full
// month/year bills exactly the configured amount.
func exact(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func TestEffectiveRateFor(t *testing.T) {
	cat := Category{DefaultMonthlyCost: 50, DefaultYearlyCost: 550}

	monthly := Vehicle{BillingPeriod: BillingMonthly}
	if got := monthly.EffectiveRateFor(cat); got != 50 {
		t.Errorf("monthly default: got %v want 50", got)
	}

	yearly := Vehicle{BillingPeriod: BillingYearly}
	if got := yearly.EffectiveRateFor(cat); got != 550 {
		t.Errorf("yearly default: got %v want 550", got)
	}

	override := 42.0
	overridden := Vehicle{BillingPeriod: BillingMonthly, CostOverride: &override}
	if got := overridden.EffectiveRateFor(cat); got != 42 {
		t.Errorf("override: got %v want 42", got)
	}
}

func TestCostInRangeMonthly(t *testing.T) {
	cat := Category{DefaultMonthlyCost: 30}
	v := Vehicle{
		BillingPeriod: BillingMonthly,
		StartDate:     date(2024, time.January, 1),
	}
	// One full year is exactly 12 monthly charges (each full month = full rate).
	got := v.CostInRange(cat, date(2024, time.January, 1), date(2025, time.January, 1))
	if !exact(got, 30*12) {
		t.Errorf("one year monthly: got %.2f want 360.00", got)
	}
}

// TestFullYearIsExact reproduces the reported case: a full calendar year on a
// yearly rate must bill exactly the configured amount (not 499.66 via 365.25).
func TestFullYearIsExact(t *testing.T) {
	cat := Category{DefaultYearlyCost: 500}
	v := Vehicle{BillingPeriod: BillingYearly, StartDate: date(2024, time.January, 1)}
	got := v.CostInRange(cat, date(2025, time.January, 1), date(2026, time.January, 1))
	if !exact(got, 500) {
		t.Errorf("full year 2025: got %.4f want 500.00", got)
	}
	// A full leap year (2024, 366 days) is also exactly the rate.
	got = v.CostInRange(cat, date(2024, time.January, 1), date(2025, time.January, 1))
	if !exact(got, 500) {
		t.Errorf("full leap year 2024: got %.4f want 500.00", got)
	}
}

// TestFullMonthIsExact: a full calendar month bills exactly the monthly rate,
// regardless of the month's length.
func TestFullMonthIsExact(t *testing.T) {
	cat := Category{DefaultMonthlyCost: 90}
	v := Vehicle{BillingPeriod: BillingMonthly, StartDate: date(2025, time.February, 1)}
	got := v.CostInRange(cat, date(2025, time.February, 1), date(2025, time.March, 1)) // 28 days
	if !exact(got, 90) {
		t.Errorf("full February: got %.4f want 90.00", got)
	}
}

func TestCostInRangeYearly(t *testing.T) {
	cat := Category{DefaultYearlyCost: 600}
	v := Vehicle{
		BillingPeriod: BillingYearly,
		StartDate:     date(2024, time.January, 1),
	}
	got := v.CostInRange(cat, date(2024, time.January, 1), date(2025, time.January, 1))
	if !exact(got, 600) {
		t.Errorf("one year yearly: got %.2f want 600.00", got)
	}
}

func TestAccruedRespectsEndDate(t *testing.T) {
	cat := Category{DefaultMonthlyCost: 30}
	// EndDate is inclusive (the vehicle occupies the space through its last day),
	// so ending on 30 Jun bills exactly Jan–Jun = 6 full months.
	end := date(2024, time.June, 30)
	v := Vehicle{
		BillingPeriod: BillingMonthly,
		StartDate:     date(2024, time.January, 1),
		EndDate:       &end,
	}
	// Exactly 6 full months of accrual regardless of a far-future asOf.
	got := v.AccruedCostAsOf(cat, date(2030, time.January, 1))
	if !exact(got, 30*6) {
		t.Errorf("capped by end date: got %.2f want 180.00", got)
	}
}

// TestProrationNoDriftManyPeriods: summing many full periods in integer cents
// must stay exact even for awkward per-cent rates (no float drift).
func TestProrationNoDriftManyPeriods(t *testing.T) {
	cat := Category{DefaultMonthlyCost: 33.33}
	v := Vehicle{BillingPeriod: BillingMonthly, StartDate: date(2000, time.January, 1)}
	// 120 full months must equal exactly 120 * 33.33 = 3999.60.
	got := v.CostInRange(cat, date(2000, time.January, 1), date(2010, time.January, 1))
	want := 33.33 * 120
	if math.Abs(got-want) > 0.001 {
		t.Errorf("120 months: got %.2f want %.2f", got, want)
	}
}

func TestCostZeroBeforeStart(t *testing.T) {
	cat := Category{DefaultMonthlyCost: 30}
	v := Vehicle{
		BillingPeriod: BillingMonthly,
		StartDate:     date(2024, time.June, 1),
	}
	got := v.CostInRange(cat, date(2024, time.January, 1), date(2024, time.March, 1))
	if got != 0 {
		t.Errorf("before start: got %.2f want 0", got)
	}
}
