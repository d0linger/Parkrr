package handlers

import (
	"testing"
	"time"

	"github.com/preining/parkrr/internal/models"
)

func TestChargeSettled(t *testing.T) {
	mustDate := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	i64p := func(v int64) *int64 { return &v }
	june := mustDate("2026-06-15") // PeriodKey (monthly) = "2026-06"

	// A monthly agreement covering all of the person's vehicles from May 2026.
	base := models.FlatRatePeriod{
		Amount:     100,
		Period:     models.BillingMonthly,
		StartDate:  mustDate("2026-05-01"),
		VehicleIDs: []int64{},
	}
	// mk clones base and applies a mutator, returning it as a one-element slice.
	mk := func(mut func(a *models.FlatRatePeriod)) []models.FlatRatePeriod {
		a := base
		a.VehicleIDs = []int64{}
		a.PaidPeriods = nil
		if mut != nil {
			mut(&a)
		}
		return []models.FlatRatePeriod{a}
	}

	cases := []struct {
		name        string
		vehicleID   *int64
		agreements  []models.FlatRatePeriod
		ownPaid     bool
		vehiclePaid bool
		want        bool
	}{
		{"standalone paid", nil, nil, true, false, true},
		{"standalone unpaid", nil, nil, false, false, false},
		{"bound, no agreements, vehicle paid", i64p(7), nil, false, true, true},
		{"bound, no agreements, vehicle unpaid", i64p(7), nil, false, false, false},
		{"bound, covered, master paid", i64p(7), mk(func(a *models.FlatRatePeriod) { a.Paid = true }), false, false, true},
		{"bound, covered, period paid", i64p(7), mk(func(a *models.FlatRatePeriod) { a.PaidPeriods = []string{"2026-06"} }), false, false, true},
		{"bound, covered, period only partially paid (fixed)", i64p(7), mk(func(a *models.FlatRatePeriod) { a.PaidFixed = map[string]float64{"2026-06": 40} }), false, false, false},
		{"bound, covered, other period paid only", i64p(7), mk(func(a *models.FlatRatePeriod) { a.PaidPeriods = []string{"2026-05"} }), false, false, false},
		{"bound, covered, period unpaid -> vehicle flag", i64p(7), mk(nil), false, true, true},
		{"bound, covered, period unpaid, vehicle unpaid", i64p(7), mk(nil), false, false, false},
		{"bound, agreement ended before date", i64p(7), mk(func(a *models.FlatRatePeriod) {
			a.Paid = true
			end := mustDate("2026-06-01")
			a.EndDate = &end
		}), false, false, false},
		{"bound, agreement doesn't cover vehicle", i64p(7), mk(func(a *models.FlatRatePeriod) {
			a.Paid = true
			a.VehicleIDs = []int64{99}
		}), false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chargeSettled(tc.agreements, tc.vehicleID, june, tc.ownPaid, tc.vehiclePaid); got != tc.want {
				t.Errorf("chargeSettled = %v, want %v", got, tc.want)
			}
		})
	}
}
