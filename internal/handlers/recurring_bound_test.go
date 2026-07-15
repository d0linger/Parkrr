package handlers

import (
	"math"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/models"
)

// TestRecurringPaidBound verifies that a vehicle-bound recurring charge's paid
// portion follows its Gefährt / covering Pauschale (not its own per-period
// flags), mirroring how a one-off bound charge settles via chargeSettled.
func TestRecurringPaidBound(t *testing.T) {
	mustDate := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	i64p := func(v int64) *int64 { return &v }
	now := mustDate("2026-06-15")
	const vid int64 = 7

	// Monthly recurring, 100/month from Jan 2026, bound to vehicle 7.
	rc := models.RecurringCharge{
		VehicleID: i64p(vid),
		Amount:    100,
		Period:    models.BillingMonthly,
		StartDate: mustDate("2026-01-01"),
	}
	rcp := rc.AsPeriod()
	accrued := rcp.AccruedAsOf(now) // Jan–May full + June prorated

	// A monthly Pauschale covering vehicle 7 from Jan 2026.
	cover := func(mut func(a *models.FlatRatePeriod)) []models.FlatRatePeriod {
		a := models.FlatRatePeriod{
			Amount: 200, Period: models.BillingMonthly,
			StartDate: mustDate("2026-01-01"), VehicleIDs: []int64{vid},
		}
		if mut != nil {
			mut(&a)
		}
		return []models.FlatRatePeriod{a}
	}
	marchCost := rcp.ElapsedPeriodCosts(now)["2026-03"]

	cases := []struct {
		name        string
		agreements  []models.FlatRatePeriod
		vehiclePaid bool
		want        float64
	}{
		{"nothing paid", cover(nil), false, 0},
		{"vehicle paid flag settles all", cover(nil), true, accrued},
		{"whole Pauschale paid settles all", cover(func(a *models.FlatRatePeriod) { a.Paid = true }), false, accrued},
		{"one Pauschale period paid settles only that period",
			cover(func(a *models.FlatRatePeriod) { a.PaidPeriods = []string{"2026-03"} }), false, marchCost},
		{"non-covering Pauschale ignored", cover(func(a *models.FlatRatePeriod) {
			a.VehicleIDs = []int64{vid + 1}
			a.Paid = true
		}), false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recurringPaidBound(&rc, tc.agreements, tc.vehiclePaid, now)
			if math.Abs(got-tc.want) > 0.01 {
				t.Errorf("recurringPaidBound = %.2f, want %.2f", got, tc.want)
			}
		})
	}

	// Sanity: March cost is a real, non-zero period so the partial case is meaningful.
	if marchCost <= 0 {
		t.Fatalf("expected a positive March period cost, got %.2f", marchCost)
	}
}

// TestRecurringPaidBoundMidPeriodStart covers a charge that begins mid-period
// with a Pauschale starting on the same day: the first (partial) period must be
// recognised as covered, not skipped because its calendar start predates the
// Pauschale.
func TestRecurringPaidBoundMidPeriodStart(t *testing.T) {
	mustDate := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	i64p := func(v int64) *int64 { return &v }
	now := mustDate("2026-06-15")
	const vid int64 = 7

	cases := []struct {
		name   string
		period string
		start  string
	}{
		{"monthly mid-month start", models.BillingMonthly, "2026-01-15"},
		{"yearly mid-year start", models.BillingYearly, "2026-03-10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := models.RecurringCharge{
				VehicleID: i64p(vid), Amount: 100, Period: tc.period, StartDate: mustDate(tc.start),
			}
			rcp := rc.AsPeriod()
			accrued := rcp.AccruedAsOf(now)
			// Pauschale covering the vehicle, starting the same day, fully paid.
			ag := []models.FlatRatePeriod{{
				Amount: 200, Period: tc.period, StartDate: mustDate(tc.start),
				VehicleIDs: []int64{vid}, Paid: true,
			}}
			got := recurringPaidBound(&rc, ag, false, now)
			if math.Abs(got-accrued) > 0.01 {
				t.Errorf("first partial period not settled: paid=%.2f, want accrued=%.2f", got, accrued)
			}
		})
	}
}

// TestRecurringSumsUnbound checks that a person-level (unbound) recurring charge
// still settles via its own flags through recurringSums.
func TestRecurringSumsUnbound(t *testing.T) {
	mustDate := func(s string) time.Time {
		d, _ := time.Parse("2006-01-02", s)
		return d
	}
	now := mustDate("2026-06-15")
	rc := models.RecurringCharge{
		Amount: 100, Period: models.BillingMonthly,
		StartDate: mustDate("2026-01-01"), Paid: true, // whole charge paid via own flag
	}
	accrued, paid := recurringSums([]models.RecurringCharge{rc}, nil, nil, now)
	if math.Abs(paid-accrued) > 0.01 {
		t.Errorf("unbound paid=%.2f should equal accrued=%.2f when own paid flag set", paid, accrued)
	}
	if accrued <= 0 {
		t.Fatalf("expected positive accrued, got %.2f", accrued)
	}
}
