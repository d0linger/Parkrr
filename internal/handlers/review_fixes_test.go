package handlers

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestAgreementMasterSliderBlockedWhenInvoiced (review #4): the master slider must
// refuse a Pauschale whose periods are billed on a non-canceled invoice — booking
// per-period payments there would double-count the invoice payment.
func TestAgreementMasterSliderBlockedWhenInvoiced(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 30, "monthly", firstOfMonthMonthsAgo(3).Format("2006-01-02"))
	createInvoice(t, h, pid) // bills the agreement's completed periods

	body, _ := json.Marshal(map[string]any{"paid": true})
	req := httptest.NewRequest(http.MethodPost, "/api/agreements/"+strconv.FormatInt(aid, 10)+"/paid", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(aid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetAgreementPaid(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("master toggle on an invoiced Pauschale must be 409, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestRecurringMasterSliderSkipsInvoicedPeriods (review #7): the recurring master
// slider must not book a payment for a period already settled by an invoice.
func TestRecurringMasterSliderSkipsInvoicedPeriods(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	rid := mkRecurring(t, h, pid, 20, firstOfMonthMonthsAgo(3).Format("2006-01-02"))
	iv := createInvoice(t, h, pid) // bills the 3 completed recurring months
	full := getInvoiceT(t, h, iv.ID)
	payInvoices(t, h, pid, map[string]any{"amount": full.Total, "auto": true})

	setRecurringMasterPaid(t, h, rid, true)

	// No real payment is booked for the invoiced periods, so the invoice payment isn't
	// duplicated. (The running month is settled off-book by the master flag.)
	var n int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='recurring' AND settles_ref=$1`, rid).Scan(&n)
	if n != 0 {
		t.Errorf("invoiced recurring periods must not be booked again, got %d settle payments", n)
	}
	// Without the skip, the 3 invoiced months (60) would be paid twice → balance ~ -60
	// (phantom Guthaben). It must net to ~0 instead.
	if bal := personStatsT(t, h, pid).Balance; math.Abs(bal) > 0.05 {
		t.Errorf("master toggle on an invoiced recurring must not double-count (balance ~0), got %.2f", bal)
	}
}

// TestReconcileAfterRestoreBooksLegacyPeriodPayments (review #6): restoring an older
// backup carries period settlements as off-book flags only; the post-restore
// reconcile must book their real payments (Backfill after Migrate) without a restart.
func TestReconcileAfterRestoreBooksLegacyPeriodPayments(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 30, "monthly", firstOfMonthMonthsAgo(3).Format("2006-01-02"))
	key := firstOfMonthMonthsAgo(3).Format("2006-01")
	// Legacy off-book settlement (as a restored pre-036 backup would leave it).
	if _, err := h.Pool.Exec(t.Context(),
		`INSERT INTO flat_rate_period_payments (period_id, period_key) VALUES ($1,$2)`, aid, key); err != nil {
		t.Fatalf("seed legacy settlement: %v", err)
	}
	var pre int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='agreement' AND settles_ref=$1`, aid).Scan(&pre)
	if pre != 0 {
		t.Fatalf("precondition: no settle payment yet, got %d", pre)
	}

	if err := h.reconcileSchemaAfterRestore(t.Context()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var post int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='agreement' AND settles_ref=$1 AND settles_period=$2`, aid, key).Scan(&post)
	if post != 1 {
		t.Errorf("reconcile after restore must book the legacy period payment, got %d", post)
	}
}
