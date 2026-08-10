package models

import (
	"math"
	"testing"
	"time"
)

func approxEuro(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// TestProrateLeapYearFebruary: the proration denominator is the ACTUAL month
// length, so the same amount over the same 14-day span costs differently in a
// 29-day (leap) vs 28-day February — and a full February always bills the amount.
func TestProrateLeapYearFebruary(t *testing.T) {
	leap := prorate(300, BillingMonthly, date(2024, time.February, 1), date(2024, time.February, 15)) // 29 days
	non := prorate(300, BillingMonthly, date(2023, time.February, 1), date(2023, time.February, 15))  // 28 days
	if !approxEuro(leap, 144.83) {
		t.Errorf("14/29 of 300 (leap Feb): got %.2f, want 144.83", leap)
	}
	if !approxEuro(non, 150.00) {
		t.Errorf("14/28 of 300 (non-leap Feb): got %.2f, want 150.00", non)
	}
	if got := prorate(300, BillingMonthly, date(2024, time.February, 1), date(2024, time.March, 1)); !approxEuro(got, 300) {
		t.Errorf("full leap February must bill exactly 300, got %.2f", got)
	}
	if got := prorate(300, BillingMonthly, date(2023, time.February, 1), date(2023, time.March, 1)); !approxEuro(got, 300) {
		t.Errorf("full non-leap February must bill exactly 300, got %.2f", got)
	}
}

// TestEndedInclusiveBoundary pins the P1-1 semantics: an agreement is still active
// ON its end date and counts as ended only the day after.
func TestEndedInclusiveBoundary(t *testing.T) {
	end := date(2026, time.March, 31)
	a := &FlatRatePeriod{EndDate: &end}
	if a.Ended(date(2026, time.March, 31)) {
		t.Error("must NOT be ended on the (inclusive) end date itself")
	}
	if !a.Ended(date(2026, time.April, 1)) {
		t.Error("must be ended the day after the end date")
	}
	if (&FlatRatePeriod{}).Ended(date(2026, time.April, 1)) {
		t.Error("an open-ended agreement is never ended")
	}
}

// TestSettledAsOf: a fully-paid agreement is settled; an unpaid one with accrued
// cost is not.
func TestSettledAsOf(t *testing.T) {
	start, end := date(2026, time.January, 1), date(2026, time.March, 31)
	paid := &FlatRatePeriod{Period: BillingMonthly, Amount: 30, StartDate: start, EndDate: &end, Paid: true}
	if !paid.SettledAsOf(date(2026, time.June, 1)) {
		t.Error("a fully-paid (master flag) agreement must be settled once ended")
	}
	unpaid := &FlatRatePeriod{Period: BillingMonthly, Amount: 30, StartDate: start, EndDate: &end}
	if unpaid.SettledAsOf(date(2026, time.June, 1)) {
		t.Error("an unpaid agreement with accrued cost must not be settled")
	}
}
