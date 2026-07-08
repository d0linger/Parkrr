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
