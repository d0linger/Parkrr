package handlers

import (
	"testing"
	"time"

	"github.com/preining/parkrr/internal/models"
)

// The master "bezahlt" of a Nebenkosten counts periods settled through a fully
// paid invoice, not only its own per-period keys. Open or canceled invoices never
// reach InvoicePaidPeriods (the query filters them), so without them the master
// stays "offen".
func TestRecurringPaidCountsFullyPaidInvoicePeriods(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	base := models.RecurringCharge{
		Amount: 20, Period: models.BillingMonthly,
		StartDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	invoiced := base
	invoiced.InvoicePaidPeriods = []string{"2026-06", "2026-07", "2026-08"}
	deriveRecurring(&invoiced, now)
	if !invoiced.Paid {
		t.Error("all completed periods are on a fully paid invoice, but the charge reads offen")
	}
	if len(invoiced.PaidPeriods) != 0 {
		t.Errorf("invoice-paid periods leaked into the own paid_periods: %v", invoiced.PaidPeriods)
	}

	mixed := base
	mixed.PaidPeriods = []string{"2026-06"}
	mixed.InvoicePaidPeriods = []string{"2026-07"}
	deriveRecurring(&mixed, now)
	if mixed.Paid {
		t.Error("August is neither paid nor on a paid invoice, but the charge reads bezahlt")
	}

	open := base
	deriveRecurring(&open, now)
	if open.Paid {
		t.Error("nothing is paid, but the charge reads bezahlt")
	}
}
