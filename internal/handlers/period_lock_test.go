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
	// CreateAgreement returns the person's full agreement list; take the newest id.
	var list []struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	var id int64
	for _, a := range list {
		if a.ID > id {
			id = a.ID
		}
	}
	return id
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

// TestPaidPauschalePeriodNotOwed (P1): marking a Pauschale period paid via the
// per-period mechanism (flat_rate_period_payments — no payments row) must reduce
// the payment-based balance. Before the fix the balance ignored it, so the period
// showed as owed forever while the invoice already omitted it (uncollectable).
func TestPaidPauschalePeriodNotOwed(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 30, "monthly", firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	before := personStatsT(t, h, pid).Balance
	key := firstOfMonthMonthsAgo(3).Format("2006-01") // an elapsed month
	body, _ := json.Marshal(map[string]any{"period_key": key, "paid": true})
	req := httptest.NewRequest(http.MethodPost, "/api/agreements/"+strconv.FormatInt(aid, 10)+"/period-paid", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(aid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetAgreementPeriodPaid(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("period-paid: %d %s", rec.Code, rec.Body.String())
	}

	after := personStatsT(t, h, pid).Balance
	if diff := before - after; math.Abs(diff-30) > 0.05 {
		t.Errorf("paying one 30€ Pauschale period must drop the balance by 30 (no phantom debt); before=%.2f after=%.2f diff=%.2f",
			before, after, diff)
	}
}

// TestInvoicedPeriodNotDoubleCredited (review D1): a period that was invoiced and
// the invoice paid is settled through the payment. Additionally tapping that
// period's "bezahlt" slider must NOT credit it a second time (personPeriodPaid
// excludes invoiced periods) — otherwise a phantom Guthaben appears.
func TestInvoicedPeriodNotDoubleCredited(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 30, "monthly", firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	iv := createInvoice(t, h, pid) // bills + locks the 3 completed months (90)
	if math.Abs(iv.Subtotal-90) > 0.05 {
		t.Fatalf("expected 90, got %.2f", iv.Subtotal)
	}
	payInvoices(t, h, pid, map[string]any{
		"amount": iv.Total, "method": "ueberweisung",
		"allocations": []map[string]any{{"invoice_id": iv.ID, "amount": iv.Total}},
	})
	before := personStatsT(t, h, pid).Balance // ~ running month only

	// Redundantly tap an INVOICED period's paid slider.
	key := firstOfMonthMonthsAgo(3).Format("2006-01")
	body, _ := json.Marshal(map[string]any{"period_key": key, "paid": true})
	req := httptest.NewRequest(http.MethodPost, "/api/agreements/"+strconv.FormatInt(aid, 10)+"/period-paid", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(aid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetAgreementPeriodPaid(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("period-paid: %d %s", rec.Code, rec.Body.String())
	}

	s := personStatsT(t, h, pid)
	if math.Abs(s.Balance-before) > 0.05 {
		t.Errorf("marking an INVOICED period paid must not change the balance; before=%.2f after=%.2f", before, s.Balance)
	}
	if s.Credit > 0.05 {
		t.Errorf("no phantom Guthaben expected after paying an invoiced+toggled period, got credit=%.2f", s.Credit)
	}
}

// TestReToggleManuallySettledChargeNoError (review P0-#2): a charge settled by a
// manual payment, toggled open, then paid again must not hit the (kind,ref_id)
// unique index and 500 — syncTogglePayment sees the existing allocation and no-ops.
func TestReToggleManuallySettledChargeNoError(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "ReToggle", 100, "2026-05-01")

	// Manual payment explicitly settles the charge (creates a manual allocation).
	pbody, _ := json.Marshal(map[string]any{
		"amount": 100, "method": "ueberweisung",
		"allocations": []map[string]any{{"kind": "charge", "id": cid}},
	})
	preq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/payments", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(pid, 10))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	h.CreatePayment(prec, preq)
	if prec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", prec.Code, prec.Body.String())
	}

	// Toggle open, then paid again — the second must not 500 on the unique index.
	setChargePaidT(t, h, cid, false)
	setChargePaidT(t, h, cid, true)
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

// mkStoredVehicle creates a stored vehicle on a fresh monthly tariff and returns
// its id and the monthly rate.
func mkStoredVehicle(t *testing.T, h *Handler, pid int64, monthly float64, start string) int64 {
	t.Helper()
	cbody, _ := json.Marshal(map[string]any{
		"name": "VehTarif-" + strconv.FormatInt(time.Now().UnixNano(), 10), "default_monthly_cost": monthly, "default_yearly_cost": monthly * 10,
	})
	crec := httptest.NewRecorder()
	h.CreateCategory(crec, httptest.NewRequest(http.MethodPost, "/api/categories", bytes.NewReader(cbody)))
	if crec.Code != http.StatusCreated {
		t.Fatalf("category: %d %s", crec.Code, crec.Body.String())
	}
	var cat struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(crec.Body.Bytes(), &cat)

	vbody, _ := json.Marshal(map[string]any{
		"person_id": pid, "category_id": cat.ID, "billing_period": "monthly",
		"status": "stored", "start_date": start, "label": "RentAuto",
	})
	vrec := httptest.NewRecorder()
	h.CreateVehicle(vrec, httptest.NewRequest(http.MethodPost, "/api/vehicles", bytes.NewReader(vbody)))
	if vrec.Code != http.StatusCreated {
		t.Fatalf("vehicle: %d %s", vrec.Code, vrec.Body.String())
	}
	var veh struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(vrec.Body.Bytes(), &veh)
	return veh.ID
}

// TestVehicleRentBilledPerCompletedPeriodThenLocked (#3): an uncovered vehicle's
// rent is billed one line per COMPLETED month; the running month is deferred; and
// a once-invoiced vehicle no longer blocks its future rent because only the billed
// periods lock (re-invoicing the same periods finds nothing open).
func TestVehicleRentBilledPerCompletedPeriodThenLocked(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)

	start := firstOfMonthMonthsAgo(3) // exactly 3 completed months
	mkStoredVehicle(t, h, pid, 30, start.Format("2006-01-02"))
	curKey := time.Now().Format("2006-01")

	iv := createInvoice(t, h, pid)
	full := getInvoiceT(t, h, iv.ID)

	if math.Abs(iv.Subtotal-90) > 0.05 {
		t.Fatalf("expected 3 completed months of rent = 90, got %.2f; items=%+v", iv.Subtotal, full.Items)
	}
	if len(full.Items) != 3 {
		t.Fatalf("expected 3 per-month rent lines, got %d: %+v", len(full.Items), full.Items)
	}
	for _, it := range full.Items {
		if strings.Contains(it.Description, curKey) {
			t.Errorf("running month %s must not be billed: %q", curKey, it.Description)
		}
	}

	// Re-invoice: the three billed months are locked, the current one is still
	// running -> nothing open -> 400 (the vehicle is not blocked wholesale).
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateInvoice(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("re-invoicing locked vehicle months must yield 400, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestPersonWidePauschaleNoDoubleBill is the review regression for finding #1: a
// person-wide Pauschale (empty vehicle_ids = covers ALL vehicles) must suppress the
// individual rent of every vehicle — otherwise the invoice bills both the Pauschale
// and each vehicle's Einstellplatz, and the slider lists phantom owed rent.
func TestPersonWidePauschaleNoDoubleBill(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)

	start := firstOfMonthMonthsAgo(3).Format("2006-01-02")
	// A vehicle on a €30/mo tariff...
	mkStoredVehicle(t, h, pid, 30, start)
	// ...and a person-wide €100/mo Pauschale (no vehicle_ids -> all vehicles).
	agBody, _ := json.Marshal(map[string]any{"amount": 100, "period": "monthly", "start_date": start})
	arec := httptest.NewRecorder()
	areq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/agreements", bytes.NewReader(agBody))
	areq.SetPathValue("id", strconv.FormatInt(pid, 10))
	h.CreateAgreement(arec, areq)
	if arec.Code != http.StatusOK && arec.Code != http.StatusCreated {
		t.Fatalf("agreement: %d %s", arec.Code, arec.Body.String())
	}

	// The open balance is Pauschale-only (vehicle covered): the invoice must match it,
	// i.e. bill 3×100 for the completed months and NO €30 Einstellplatz line.
	iv := createInvoice(t, h, pid)
	full := getInvoiceT(t, h, iv.ID)
	if math.Abs(iv.Subtotal-300) > 0.05 {
		t.Fatalf("person-wide Pauschale must bill 3×100=300 with no vehicle rent, got %.2f; items=%+v",
			iv.Subtotal, full.Items)
	}
	for _, it := range full.Items {
		if strings.Contains(it.Description, "Einstellplatz") {
			t.Errorf("covered vehicle rent must not be billed: %q", it.Description)
		}
	}

	// The covered vehicle must also not appear as a phantom owed item on the slider.
	if s := personStatsT(t, h, pid); s.Balance < -0.05 || s.Credit > 0.05 {
		t.Errorf("no phantom credit expected, balance=%.2f credit=%.2f", s.Balance, s.Credit)
	}
}

// TestVehicleSliderInertAfterInvoicing (#3): once a vehicle has been invoiced,
// its "bezahlt" slider must not mint an auto-payment on top of the open invoice —
// the invoiced vehicle drops off the manual money path (settle via the invoice).
func TestVehicleSliderInertAfterInvoicing(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))
	createInvoice(t, h, pid) // locks the completed months

	before := personStatsT(t, h, pid)
	body, _ := json.Marshal(map[string]any{"paid": true})
	req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/paid", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.MarkPaid(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("markpaid: %d %s", rec.Code, rec.Body.String())
	}
	after := personStatsT(t, h, pid)
	if math.Abs(after.PaymentsTotal-before.PaymentsTotal) > 0.005 {
		t.Errorf("slider must not record a payment for an invoiced vehicle: %.2f -> %.2f",
			before.PaymentsTotal, after.PaymentsTotal)
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
