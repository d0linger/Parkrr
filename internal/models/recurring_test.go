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
}
