package models

import (
	"testing"
	"time"
)

func TestAgreementFullYearIsExact(t *testing.T) {
	a := FlatRatePeriod{Amount: 500, Period: BillingYearly, StartDate: date(2025, time.January, 1)}
	got := a.CostInRange(date(2025, time.January, 1), date(2026, time.January, 1))
	if got != 500 {
		t.Errorf("full-year agreement: got %.2f want 500.00", got)
	}
}

func TestAgreementCovers(t *testing.T) {
	all := FlatRatePeriod{}
	if !all.Covers(7) {
		t.Error("empty VehicleIDs must cover all vehicles")
	}
	some := FlatRatePeriod{VehicleIDs: []int64{1, 3}}
	if !some.Covers(3) || some.Covers(2) {
		t.Error("explicit VehicleIDs must cover only listed vehicles")
	}
}

func TestVehicleUncoveredCostSubtractsCoveredWindow(t *testing.T) {
	cat := Category{DefaultMonthlyCost: 100}
	v := Vehicle{BillingPeriod: BillingMonthly, StartDate: date(2025, time.January, 1)}
	from, to := date(2025, time.January, 1), date(2025, time.March, 1) // Jan+Feb = 200

	if full := v.CostInRange(cat, from, to); full != 200 {
		t.Fatalf("precondition: full cost got %.2f want 200.00", full)
	}
	// Agreement covers January only -> February (100) remains per-vehicle.
	agr := FlatRatePeriod{StartDate: date(2025, time.January, 1), EndDate: tp(date(2025, time.February, 1))}
	got := VehicleUncoveredCost(&v, cat, from, to, []FlatRatePeriod{agr})
	if got != 100 {
		t.Errorf("uncovered cost: got %.2f want 100.00", got)
	}
}

func TestVehicleFullyCoveredCostsZero(t *testing.T) {
	cat := Category{DefaultMonthlyCost: 90}
	v := Vehicle{BillingPeriod: BillingMonthly, StartDate: date(2025, time.January, 1)}
	from, to := date(2025, time.January, 1), date(2025, time.February, 1)
	agr := FlatRatePeriod{StartDate: date(2025, time.January, 1), EndDate: tp(date(2025, time.February, 1))}
	if got := VehicleUncoveredCost(&v, cat, from, to, []FlatRatePeriod{agr}); got != 0 {
		t.Errorf("fully covered vehicle should cost 0, got %.2f", got)
	}
}

func TestPeriodKey(t *testing.T) {
	y := FlatRatePeriod{Period: BillingYearly}
	if got := y.PeriodKey(date(2026, time.March, 4)); got != "2026" {
		t.Errorf("yearly key: got %q want 2026", got)
	}
	m := FlatRatePeriod{Period: BillingMonthly}
	if got := m.PeriodKey(date(2026, time.March, 4)); got != "2026-03" {
		t.Errorf("monthly key: got %q want 2026-03", got)
	}
}

// The master Paid flag must count every sub-period as paid, exactly matching the
// full accrued cost — this preserves legacy fully-paid billing.
func TestPaidCentsMasterMatchesCost(t *testing.T) {
	a := FlatRatePeriod{Amount: 120, Period: BillingMonthly, StartDate: date(2025, time.January, 1), Paid: true}
	from, to := date(2025, time.January, 1), date(2025, time.April, 1) // 3 months = 360
	if cost := a.CostInRange(from, to); cost != 360 {
		t.Fatalf("precondition cost got %.2f want 360.00", cost)
	}
	if cents := a.PaidCentsInRange(from, to); cents != 36000 {
		t.Errorf("master-paid cents got %d want 36000", cents)
	}
}

// Marking a single month paid must credit only that month's cost.
func TestPaidCentsPerPeriod(t *testing.T) {
	a := FlatRatePeriod{Amount: 120, Period: BillingMonthly, StartDate: date(2025, time.January, 1),
		PaidPeriods: []string{"2025-02"}}
	from, to := date(2025, time.January, 1), date(2025, time.April, 1)
	if cents := a.PaidCentsInRange(from, to); cents != 12000 {
		t.Errorf("one-month-paid cents got %d want 12000", cents)
	}
}

func TestElapsedPeriodCostsSumToAccrued(t *testing.T) {
	a := FlatRatePeriod{Amount: 500, Period: BillingYearly, StartDate: date(2025, time.January, 1)}
	asOf := date(2026, time.July, 9)
	costs := a.ElapsedPeriodCosts(asOf)
	if costs["2025"] != 500 {
		t.Errorf("2025 full year got %.2f want 500.00", costs["2025"])
	}
	var sum float64
	for _, c := range costs {
		sum += c
	}
	if diff := sum - a.AccruedAsOf(asOf); diff > 0.005 || diff < -0.005 {
		t.Errorf("period costs sum %.2f != accrued %.2f", sum, a.AccruedAsOf(asOf))
	}
}

func TestElapsedPeriodKeys(t *testing.T) {
	a := FlatRatePeriod{Period: BillingMonthly, StartDate: date(2025, time.January, 1)}
	got := a.ElapsedPeriodKeys(date(2025, time.March, 15))
	want := []string{"2025-01", "2025-02", "2025-03"}
	if len(got) != len(want) {
		t.Fatalf("elapsed keys got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("elapsed keys got %v want %v", got, want)
		}
	}
}
