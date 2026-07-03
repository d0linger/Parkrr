package models

import (
	"math"
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// approx allows a small relative tolerance because costs are prorated using an
// average year length (365.25 days), so exact-year spans over leap years differ
// slightly from a round multiple of the rate.
func approx(a, b float64) bool { return math.Abs(a-b) <= 0.02*b+0.01 }

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
	// One full year should be ~12 monthly charges.
	got := v.CostInRange(cat, date(2024, time.January, 1), date(2025, time.January, 1))
	if !approx(got, 30*12) {
		t.Errorf("one year monthly: got %.2f want ~360", got)
	}
}

func TestCostInRangeYearly(t *testing.T) {
	cat := Category{DefaultYearlyCost: 600}
	v := Vehicle{
		BillingPeriod: BillingYearly,
		StartDate:     date(2024, time.January, 1),
	}
	got := v.CostInRange(cat, date(2024, time.January, 1), date(2025, time.January, 1))
	if !approx(got, 600) {
		t.Errorf("one year yearly: got %.2f want ~600", got)
	}
}

func TestAccruedRespectsEndDate(t *testing.T) {
	cat := Category{DefaultMonthlyCost: 30}
	end := date(2024, time.July, 1)
	v := Vehicle{
		BillingPeriod: BillingMonthly,
		StartDate:     date(2024, time.January, 1),
		EndDate:       &end,
	}
	// ~6 months of accrual regardless of a far-future asOf.
	got := v.AccruedCostAsOf(cat, date(2030, time.January, 1))
	if !approx(got, 30*6) {
		t.Errorf("capped by end date: got %.2f want ~180", got)
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
