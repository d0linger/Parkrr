package handlers

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// mkAgreement creates a person-level flat-rate Pauschale and returns its id.
func mkAgreement(t *testing.T, h *Handler, pid int64, amount float64, period, start string) int64 {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"amount": amount, "period": period, "start_date": start,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/agreements", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateAgreement(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create agreement: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.ID
}

// firstOfMonthMonthsAgo returns the first day of the month n months before now,
// so period expectations are independent of the wall clock.
func firstOfMonthMonthsAgo(n int) time.Time {
	now := time.Now()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return first.AddDate(0, -n, 0)
}

// TestPauschaleBilledPerCompletedPeriodThenLocked (B1-Rest): a monthly Pauschale
// is invoiced one line per COMPLETED sub-period; the running current month is left
// out; and a second invoice finds nothing open because every completed period is
// now locked against double-fakturierung.
func TestPauschaleBilledPerCompletedPeriodThenLocked(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)

	// Start on the 1st, three whole months back -> exactly 3 completed periods.
	start := firstOfMonthMonthsAgo(3)
	mkAgreement(t, h, pid, 30, "monthly", start.Format("2006-01-02"))
	curKey := time.Now().Format("2006-01")

	iv := createInvoice(t, h, pid)
	full := getInvoiceT(t, h, iv.ID)

	// Three completed months at 30 each; running month excluded.
	if math.Abs(iv.Subtotal-90) > 0.005 {
		t.Fatalf("expected subtotal 90 (3 completed months), got %.2f; items=%+v", iv.Subtotal, full.Items)
	}
	if len(full.Items) != 3 {
		t.Fatalf("expected 3 per-period lines, got %d: %+v", len(full.Items), full.Items)
	}
	for _, it := range full.Items {
		// The current (incomplete) month must never appear.
		if strings.Contains(it.Description, curKey) {
			t.Errorf("running month %s must not be billed: %q", curKey, it.Description)
		}
	}

	// Second invoice: all completed periods are locked, nothing open -> 400.
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateInvoice(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("re-invoicing locked periods must yield 400, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestStornoReleasesPeriodLocks (B1-Rest): cancelling the invoice releases its
// per-period locks so the same periods become fakturierbar again (BAO Storno).
func TestStornoReleasesPeriodLocks(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	mkAgreement(t, h, pid, 30, "monthly", firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	iv := createInvoice(t, h, pid)
	if math.Abs(iv.Subtotal-90) > 0.005 {
		t.Fatalf("expected 90, got %.2f", iv.Subtotal)
	}

	creq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/cancel", nil)
	creq.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	crec := httptest.NewRecorder()
	h.CancelInvoice(crec, creq)
	if crec.Code != http.StatusCreated && crec.Code != http.StatusOK {
		t.Fatalf("storno: %d %s", crec.Code, crec.Body.String())
	}

	// After Storno the periods are open again -> a fresh invoice bills the same 90.
	iv2 := createInvoice(t, h, pid)
	if math.Abs(iv2.Subtotal-90) > 0.005 {
		t.Errorf("after Storno the released periods must re-bill 90, got %.2f", iv2.Subtotal)
	}
}

// TestRecurringBilledPerCompletedPeriodThenLocked (B1-Rest): a person-level
// recurring cost locks per completed sub-period too, via its real id — so it is
// not billed twice and the running month is deferred.
func TestRecurringBilledPerCompletedPeriodThenLocked(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)

	amt := 20.0
	body, _ := json.Marshal(recurringRequest{
		Description: "Strom", Amount: &amt, Period: "monthly",
		StartDate: firstOfMonthMonthsAgo(3).Format("2006-01-02"),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/recurring", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateRecurringCharge(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create recurring: %d %s", rec.Code, rec.Body.String())
	}

	iv := createInvoice(t, h, pid)
	if math.Abs(iv.Subtotal-60) > 0.005 { // 3 completed months * 20
		t.Fatalf("expected recurring subtotal 60, got %.2f", iv.Subtotal)
	}
	// Re-invoice: every completed period locked -> 400.
	req2 := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices", bytes.NewReader([]byte(`{}`)))
	req2.SetPathValue("id", strconv.FormatInt(pid, 10))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	h.CreateInvoice(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("re-invoicing locked recurring periods must yield 400, got %d %s", rec2.Code, rec2.Body.String())
	}
}
