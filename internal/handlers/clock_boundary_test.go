package handlers

import (
	"math"
	"testing"
	"time"
)

// TestBillingDeferralIsClockDeterministic pins Handler.Now to a set of awkward
// calendar dates and asserts the invoice always bills exactly the 3 completed
// months and defers the running one — regardless of what day the suite runs on.
//
// This is the guard the 2026-08-31 bug slipped through: the period-lock tests were
// green 30 days a month and red on the last, because they could only ever exercise
// the real wall clock. With an injectable clock the month-end, leap-day and
// year-roll boundaries are all testable head-on (finding FIN-12).
func TestBillingDeferralIsClockDeterministic(t *testing.T) {
	for _, asOf := range []string{
		"2026-08-31", // month end (the original bug)
		"2026-08-15", // mid month (control)
		"2024-02-29", // leap day
		"2026-12-31", // year end
		"2026-03-01", // first of a month
	} {
		t.Run(asOf, func(t *testing.T) {
			h := testHandler(t)
			pin, err := time.ParseInLocation("2006-01-02", asOf, time.Local)
			if err != nil {
				t.Fatalf("bad pin %q: %v", asOf, err)
			}
			// Mid-day on the pinned date, so a naive "end of day" boundary can't hide.
			h.Now = func() time.Time { return pin.Add(13 * time.Hour) }

			compliantSeller(t, h)
			pid := createIntegrationPerson(t, h)

			// Start on the 1st, three whole months before the pinned month -> exactly
			// three completed periods, the running (pinned) month still open.
			start := time.Date(pin.Year(), pin.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -3, 0)
			mkAgreement(t, h, pid, 30, "monthly", start.Format("2006-01-02"))

			iv := createInvoice(t, h, pid)
			if math.Abs(iv.Subtotal-90) > 0.005 {
				t.Fatalf("asOf %s: expected subtotal 90 (3 completed months, running month deferred), got %.2f",
					asOf, iv.Subtotal)
			}
		})
	}
}
