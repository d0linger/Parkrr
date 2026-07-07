package models

import (
	"testing"
	"time"
)

func f64(v float64) *float64    { return &v }
func tp(t time.Time) *time.Time { return &t }

func TestFlatRateActive(t *testing.T) {
	start := date(2025, time.January, 1)
	cases := []struct {
		name string
		p    Person
		want bool
	}{
		{"unset", Person{}, false},
		{"zero amount", Person{FlatRate: f64(0), FlatRateStart: tp(start)}, false},
		{"no start", Person{FlatRate: f64(100)}, false},
		{"active", Person{FlatRate: f64(100), FlatRateStart: tp(start)}, true},
	}
	for _, c := range cases {
		if got := c.p.FlatRateActive(); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestFlatRateInRangeFullYear(t *testing.T) {
	p := Person{
		FlatRate:       f64(600),
		FlatRatePeriod: BillingYearly,
		FlatRateStart:  tp(date(2025, time.January, 1)),
	}
	got := p.FlatRateInRange(date(2025, time.January, 1), date(2026, time.January, 1))
	if got != 600 {
		t.Errorf("full year flat rate: got %.2f want 600.00", got)
	}
}

func TestFlatRateRespectsEndAndStart(t *testing.T) {
	end := date(2025, time.July, 1)
	p := Person{
		FlatRate:       f64(120),
		FlatRatePeriod: BillingMonthly,
		FlatRateStart:  tp(date(2025, time.January, 1)),
		FlatRateEnd:    tp(end),
	}
	// Six full months from Jan through Jun, capped by the end date.
	got := p.FlatRateAccruedAsOf(date(2030, time.January, 1))
	if got != 120*6 {
		t.Errorf("capped flat rate: got %.2f want %.2f", got, float64(120*6))
	}
}

func TestFlatRateInactiveIsZero(t *testing.T) {
	p := Person{}
	if got := p.FlatRateInRange(date(2025, time.January, 1), date(2026, time.January, 1)); got != 0 {
		t.Errorf("inactive flat rate should be 0, got %.2f", got)
	}
}
