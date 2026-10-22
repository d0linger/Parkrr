package models

import (
	"math/rand"
	"testing"
	"time"
)

func ymd(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// Wertsicherung (Ausbau 29): ab dem Monatsersten gilt der neue Satz, davor der alte.
func TestRateChangeAppliesFromItsDate(t *testing.T) {
	v := Vehicle{Rate: 60, BillingPeriod: BillingMonthly, StartDate: ymd("2026-01-01"),
		RateChanges: []RateChange{{From: ymd("2026-11-01"), Amount: 66}}}
	cat := Category{}
	if got := v.CostInRange(cat, ymd("2026-10-01"), ymd("2026-11-01")); got != 60 {
		t.Errorf("Oktober: %v, want 60", got)
	}
	if got := v.CostInRange(cat, ymd("2026-11-01"), ymd("2026-12-01")); got != 66 {
		t.Errorf("November: %v, want 66", got)
	}
	if got := v.RateAt(cat, ymd("2026-10-31")); got != 60 {
		t.Errorf("RateAt vor der Änderung: %v", got)
	}
	if got := v.RateAt(cat, ymd("2026-11-01")); got != 66 {
		t.Errorf("RateAt ab der Änderung: %v", got)
	}
	p := v.AsPeriod(cat)
	costs := p.ElapsedPeriodCosts(ymd("2026-11-30"))
	if costs["2026-10"] != 60 || costs["2026-11"] != 66 {
		t.Errorf("Periodenkosten: %v", costs)
	}
}

// Ruhezeit: 10 von 31 Julitagen ruhen, berechnet werden 21/31 des Satzes.
func TestPauseSkipsItsDays(t *testing.T) {
	v := Vehicle{Rate: 62, BillingPeriod: BillingMonthly, StartDate: ymd("2026-01-01"),
		Pauses: []Pause{{From: ymd("2026-07-10"), Until: ymd("2026-07-19")}}}
	if got := v.CostInRange(Category{}, ymd("2026-07-01"), ymd("2026-08-01")); got != 42 {
		t.Errorf("Juli mit Ruhezeit: %v, want 42 (62 × 21/31)", got)
	}
	if got := v.CostInRange(Category{}, ymd("2026-08-01"), ymd("2026-09-01")); got != 62 {
		t.Errorf("August ohne Ruhezeit: %v, want 62", got)
	}
}

// Alle Rechenwege müssen sich centgenau decken: Gesamtkosten = Summe der
// Periodenkosten = bezahlte Cent bei voll bezahlten Perioden — mit beliebigen
// Preisänderungen und Ruhezeiten.
func TestSegmentsKeepEveryPathConsistent(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	for i := 0; i < 300; i++ {
		start := ymd("2025-01-01").AddDate(0, 0, rng.Intn(200))
		period := BillingMonthly
		if rng.Intn(4) == 0 {
			period = BillingYearly
		}
		a := FlatRatePeriod{Amount: float64(rng.Intn(20000)) / 100, Period: period, StartDate: start}
		for k := rng.Intn(3); k > 0; k-- {
			a.RateChanges = append(a.RateChanges, RateChange{From: start.AddDate(0, rng.Intn(18), 0), Amount: float64(rng.Intn(20000)) / 100})
		}
		for k := rng.Intn(3); k > 0; k-- {
			from := start.AddDate(0, 0, rng.Intn(500))
			a.Pauses = append(a.Pauses, Pause{From: from, Until: from.AddDate(0, 0, rng.Intn(60))})
		}
		if rng.Intn(2) == 0 {
			end := start.AddDate(0, 0, 100+rng.Intn(500))
			a.EndDate = &end
		}
		asOf := ymd("2026-12-31")
		total := toCents(a.AccruedAsOf(asOf))
		var sum int64
		var keys []string
		for k, c := range a.ElapsedPeriodCosts(asOf) {
			sum += toCents(c)
			keys = append(keys, k)
		}
		if sum != total {
			t.Fatalf("Fall %d: Summe der Perioden %d ≠ Gesamt %d (%+v)", i, sum, total, a)
		}
		var detailed int64
		for _, per := range a.ElapsedPeriodsDetailed(asOf) {
			detailed += toCents(per.Cost)
		}
		if detailed != total {
			t.Fatalf("Fall %d: Detailperioden %d ≠ Gesamt %d", i, detailed, total)
		}
		a.PaidPeriods = keys
		if paid := a.PaidCentsInRange(a.StartDate, DayAfter(asOf)); paid != total {
			t.Fatalf("Fall %d: voll bezahlt %d ≠ Gesamt %d", i, paid, total)
		}
	}
}
