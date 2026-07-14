package models

import (
	"math"
	"testing"
	"time"
)

func TestRecurringChargeAsPeriod(t *testing.T) {
	d := func(s string) time.Time {
		x, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	// 25/month from 1 May; through the end of June is exactly May+June = 50.
	rc := RecurringCharge{Amount: 25, Period: BillingMonthly, StartDate: d("2026-05-01")}
	asOf := d("2026-06-30")
	until := asOf.AddDate(0, 0, 1)

	p := rc.AsPeriod()
	if acc := p.AccruedAsOf(asOf); math.Abs(acc-50) > 0.001 {
		t.Fatalf("accrued = %v, want 50", acc)
	}

	// A whole-period payment for June credits June's 25.
	rc.PaidPeriods = []string{"2026-06"}
	p = rc.AsPeriod()
	if paid := float64(p.PaidCentsInRange(rc.StartDate, until)) / 100; math.Abs(paid-25) > 0.001 {
		t.Fatalf("period-paid = %v, want 25", paid)
	}

	// The master flag pays everything accrued.
	rc.PaidPeriods = nil
	rc.Paid = true
	p = rc.AsPeriod()
	if paid := float64(p.PaidCentsInRange(rc.StartDate, until)) / 100; math.Abs(paid-50) > 0.001 {
		t.Fatalf("master-paid = %v, want 50", paid)
	}

	// Yearly: 120/year from 1 Jan accrues the full 120 through year end.
	yr := RecurringCharge{Amount: 120, Period: BillingYearly, StartDate: d("2026-01-01")}
	yAsOf := d("2026-12-31")
	yUntil := yAsOf.AddDate(0, 0, 1)
	py := yr.AsPeriod()
	if acc := py.AccruedAsOf(yAsOf); math.Abs(acc-120) > 0.001 {
		t.Fatalf("yearly accrued = %v, want 120", acc)
	}
	// A fixed partial payment for the year credits exactly that amount.
	yr.PaidFixed = map[string]float64{"2026": 40}
	py = yr.AsPeriod()
	if paid := float64(py.PaidCentsInRange(yr.StartDate, yUntil)) / 100; math.Abs(paid-40) > 0.001 {
		t.Fatalf("yearly fixed-partial paid = %v, want 40", paid)
	}
}
