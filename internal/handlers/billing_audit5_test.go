package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/preining/parkrr/internal/models"
)

// TestUpdateInvoicedVehicleBillingFrozen (review #1): changing a billing-defining
// field (rate) on an already-invoiced vehicle must be blocked — otherwise a
// period-key/rate change re-bills or phantom-credits an invoiced span.
func TestUpdateInvoicedVehicleBillingFrozen(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(2).Format("2006-01-02"))
	createInvoice(t, h, pid) // locks the vehicle's completed periods

	lrec := httptest.NewRecorder()
	h.ListVehicles(lrec, httptest.NewRequest(http.MethodGet, "/api/vehicles?person_id="+strconv.FormatInt(pid, 10), nil))
	var vehs []models.Vehicle
	_ = json.Unmarshal(lrec.Body.Bytes(), &vehs)
	var v models.Vehicle
	for _, x := range vehs {
		if x.ID == vid {
			v = x
		}
	}
	if v.ID == 0 {
		t.Fatalf("vehicle %d not found in list", vid)
	}

	ubody, _ := json.Marshal(map[string]any{
		"person_id": v.PersonID, "category_id": v.CategoryID, "billing_period": v.BillingPeriod,
		"status": v.Status, "start_date": v.StartDate.Format("2006-01-02"), "rate": v.Rate + 20,
	})
	ureq := httptest.NewRequest(http.MethodPut, "/api/vehicles/"+strconv.FormatInt(vid, 10), bytes.NewReader(ubody))
	ureq.SetPathValue("id", strconv.FormatInt(vid, 10))
	ureq.Header.Set("Content-Type", "application/json")
	urec := httptest.NewRecorder()
	h.UpdateVehicle(urec, ureq)
	if urec.Code != http.StatusConflict {
		t.Errorf("changing rate on an invoiced vehicle must be blocked (409), got %d %s", urec.Code, urec.Body.String())
	}
}

// TestPaymentCreatedByNullable (review #1 / migration 035): the immutability guard
// must tolerate created_by being nulled — that is the ON DELETE SET NULL FK action
// fired when an admin user who booked payments is deleted; without it the user is
// undeletable. Tampering with a real money field must still be rejected.
func TestPaymentCreatedByNullable(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	pbody, _ := json.Marshal(map[string]any{"amount": 40, "method": "bar"})
	preq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/payments", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(pid, 10))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	h.CreatePayment(prec, preq)
	if prec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", prec.Code, prec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(prec.Body.Bytes(), &resp)
	payID := int64(resp["id"].(float64))
	ctx := context.Background()

	if _, err := h.Pool.Exec(ctx, `UPDATE payments SET created_by = NULL WHERE id=$1`, payID); err != nil {
		t.Errorf("nulling created_by (FK action on user delete) must be allowed, got %v", err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE payments SET amount = amount + 1 WHERE id=$1`, payID); err == nil {
		t.Errorf("changing a payment amount must still be rejected")
	}
}

// TestUnmarkInvoicedPeriodBlocked (review #2): un-marking a fixed partial on an
// already-invoiced period must be blocked — the invoice billed cost − partial, so
// dropping the partial would leave a phantom debt equal to it.
func TestUnmarkInvoicedPeriodBlocked(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 100, "monthly", firstOfMonthMonthsAgo(2).Format("2006-01-02"))
	key := firstOfMonthMonthsAgo(1).Format("2006-01")

	setPeriodPaid := func(paid bool, amount any) *httptest.ResponseRecorder {
		payload := map[string]any{"period_key": key, "paid": paid}
		if amount != nil {
			payload["amount"] = amount
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/api/agreements/"+strconv.FormatInt(aid, 10)+"/period-paid", bytes.NewReader(body))
		req.SetPathValue("id", strconv.FormatInt(aid, 10))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.SetAgreementPeriodPaid(rec, req)
		return rec
	}

	if rec := setPeriodPaid(true, 30); rec.Code != http.StatusOK {
		t.Fatalf("mark partial: %d %s", rec.Code, rec.Body.String())
	}
	createInvoice(t, h, pid) // bills cost−30 for the period, locks it

	if rec := setPeriodPaid(false, nil); rec.Code != http.StatusConflict {
		t.Errorf("un-marking an invoiced period must be blocked (409), got %d %s", rec.Code, rec.Body.String())
	}
}

// TestPaidFixedPartialSurvivesInvoicing (5th-pass A1-1): a per-period fixed
// partial (Teilbetrag) must stay credited after its period is invoiced — the
// invoice bills cost − partial, so dropping the credit on lock creates a permanent
// phantom debt equal to the partial. Since Fix 1 the partial is a REAL Zahlungseingang
// (PaymentsTotal), not an off-book PeriodPaid credit, so the invariant is checked
// mechanism-agnostically: the balance must be unchanged across invoicing, and the
// €30 must remain recorded as a real payment.
func TestPaidFixedPartialSurvivesInvoicing(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 100, "monthly", firstOfMonthMonthsAgo(2).Format("2006-01-02"))

	// Record a €30 fixed partial on a completed month.
	key := firstOfMonthMonthsAgo(1).Format("2006-01")
	body, _ := json.Marshal(map[string]any{"period_key": key, "paid": true, "amount": 30})
	req := httptest.NewRequest(http.MethodPost, "/api/agreements/"+strconv.FormatInt(aid, 10)+"/period-paid", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(aid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetAgreementPeriodPaid(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("period-paid: %d %s", rec.Code, rec.Body.String())
	}

	before := personStatsT(t, h, pid)
	// The partial is a real €30 payment (Fix 1), whichever credit path carries it.
	if credit := before.PaymentsTotal + before.PeriodPaid; math.Abs(credit-30) > 0.5 {
		t.Fatalf("the €30 partial should be credited before invoicing, got payments=%.2f periodpaid=%.2f", before.PaymentsTotal, before.PeriodPaid)
	}

	createInvoice(t, h, pid) // bills the completed periods (this one at cost − 30), locks them

	after := personStatsT(t, h, pid)
	// No phantom debt: invoicing a partially-paid period is balance-neutral.
	if math.Abs(after.Balance-before.Balance) > 0.5 {
		t.Errorf("fixed partial must survive invoicing (no phantom debt): balance before=%.2f after=%.2f", before.Balance, after.Balance)
	}
	if credit := after.PaymentsTotal + after.PeriodPaid; math.Abs(credit-30) > 0.5 {
		t.Errorf("the €30 partial credit must persist after invoicing, got payments=%.2f periodpaid=%.2f", after.PaymentsTotal, after.PeriodPaid)
	}
}

// TestStornoCarriesLeistungszeitraum (5th-pass A3-2 / §11 Abs 1 Z 4): a Storno is
// itself a Rechnung and must repeat the mandatory service period of the original.
func TestStornoCarriesLeistungszeitraum(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)
	if iv.LeistungFrom == nil {
		t.Fatalf("precondition: original invoice should carry a Leistungszeitraum")
	}

	creq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/cancel", nil)
	creq.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	crec := httptest.NewRecorder()
	h.CancelInvoice(crec, creq)
	if crec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", crec.Code, crec.Body.String())
	}
	var storno invoice
	if err := json.Unmarshal(crec.Body.Bytes(), &storno); err != nil {
		t.Fatalf("decode storno: %v", err)
	}
	full := getInvoiceT(t, h, storno.ID)
	if full.LeistungFrom == nil || full.LeistungTo == nil {
		t.Errorf("Storno must carry the Leistungszeitraum (§11 Abs 1 Z 4); from=%v to=%v", full.LeistungFrom, full.LeistungTo)
	}
}

// TestReturnToStoredClearsEndDate (5th-pass A2-5): re-opening a collected vehicle
// to "stored" must clear the stale end_date, otherwise accrual stays frozen and
// the active-looking vehicle silently stops billing.
func TestReturnToStoredClearsEndDate(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	// Collect with a PAST date → accrual capped there.
	setStatus(t, h, vid, "collected", firstOfMonthMonthsAgo(1).Format("2006-01-02"))
	collected := personStatsT(t, h, pid).Balance

	// Re-open to stored (no date) → end_date cleared, accrual resumes to today.
	setStatus(t, h, vid, "stored", "")
	stored := personStatsT(t, h, pid).Balance

	if stored <= collected+10 {
		t.Errorf("returning to stored must clear end_date and resume accrual: collected=%.2f stored=%.2f", collected, stored)
	}
}

// TestDeleteInvoicedAgreementBlocked (5th-pass A2-3): an invoiced Pauschale must
// not be deletable — it would orphan the invoice_source lock and sever the
// invoice→source reconstruction trail.
func TestDeleteInvoicedAgreementBlocked(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 100, "monthly", firstOfMonthMonthsAgo(2).Format("2006-01-02"))
	createInvoice(t, h, pid) // locks the agreement's completed periods

	dreq := httptest.NewRequest(http.MethodDelete, "/api/agreements/"+strconv.FormatInt(aid, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(aid, 10))
	drec := httptest.NewRecorder()
	h.DeleteAgreement(drec, dreq)
	if drec.Code != http.StatusConflict {
		t.Errorf("deleting an invoiced Pauschale must be blocked (409), got %d %s", drec.Code, drec.Body.String())
	}
}

// TestRebindPaidRecurringBlocked (5th-pass A2-4): re-binding a recurring charge
// that has a paid period must be refused — the rebind wipes the per-period
// settlement flags (the only record a person-level charge was paid), which would
// silently reopen already-paid periods and re-bill the customer.
func TestRebindPaidRecurringBlocked(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)

	amt := 20.0
	start := firstOfMonthMonthsAgo(3).Format("2006-01-02")
	body, _ := json.Marshal(recurringRequest{Description: "Strom", Amount: &amt, Period: "monthly", StartDate: start})
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/recurring", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateRecurringCharge(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create recurring: %d %s", rec.Code, rec.Body.String())
	}
	var cr struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &cr)

	// Mark a completed period paid (off-book per-period settlement).
	key := firstOfMonthMonthsAgo(3).Format("2006-01")
	pbody, _ := json.Marshal(map[string]any{"period_key": key, "paid": true})
	preq := httptest.NewRequest(http.MethodPost, "/api/recurring/"+strconv.FormatInt(cr.ID, 10)+"/period-paid", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(cr.ID, 10))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	h.SetRecurringChargePeriodPaid(prec, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("period-paid: %d %s", prec.Code, prec.Body.String())
	}

	vid := mkStoredVehicle(t, h, pid, 30, start)

	// Rebinding onto the vehicle must be refused while the paid period exists.
	ubody, _ := json.Marshal(recurringRequest{Description: "Strom", Amount: &amt, Period: "monthly", StartDate: start, VehicleID: &vid})
	ureq := httptest.NewRequest(http.MethodPut, "/api/recurring/"+strconv.FormatInt(cr.ID, 10), bytes.NewReader(ubody))
	ureq.SetPathValue("id", strconv.FormatInt(cr.ID, 10))
	ureq.Header.Set("Content-Type", "application/json")
	urec := httptest.NewRecorder()
	h.UpdateRecurringCharge(urec, ureq)
	if urec.Code != http.StatusConflict {
		t.Errorf("rebinding a recurring charge with a paid period must be blocked (409), got %d %s", urec.Code, urec.Body.String())
	}
}

// TestBillingLifecycleReconciles is the capstone cross-cutting number check: it
// walks a charge through invoice → full payment → Storno and asserts the balance
// identity holds and money is conserved at every step.
func TestBillingLifecycleReconciles(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)

	// (1) Open charge → owed 100.
	if s := personStatsT(t, h, pid); math.Abs(s.Balance-100) > 0.005 {
		t.Fatalf("after charge: balance %.2f, want 100", s.Balance)
	}

	// (2) Invoice → balance = net + USt = invoice total; invoiced_tax = the USt.
	iv := createInvoice(t, h, pid)
	s := personStatsT(t, h, pid)
	if math.Abs(s.Balance-iv.Total) > 0.005 {
		t.Errorf("after invoice: balance %.2f, want invoice total %.2f", s.Balance, iv.Total)
	}
	if math.Abs(s.InvoicedTax-iv.TaxAmount) > 0.005 {
		t.Errorf("invoiced_tax %.2f, want %.2f", s.InvoicedTax, iv.TaxAmount)
	}

	// (3) Pay the invoice in full → balance 0.
	payInvoices(t, h, pid, map[string]any{"amount": iv.Total, "method": "ueberweisung", "auto": true})
	if s := personStatsT(t, h, pid); math.Abs(s.Balance) > 0.005 {
		t.Errorf("after full payment: balance %.2f, want 0", s.Balance)
	}

	// (4) Storno → the payment is retained on-account, the net charge re-opens;
	// balance = 100 (charge) − iv.Total (payment) = −USt → a Guthaben of the tax.
	// Money is conserved: paid iv.Total, now owes net 100, overpaid by the USt.
	creq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/cancel", nil)
	creq.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	crec := httptest.NewRecorder()
	h.CancelInvoice(crec, creq)
	if crec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", crec.Code, crec.Body.String())
	}
	s = personStatsT(t, h, pid)
	wantBal := round2(100 - iv.Total)
	if math.Abs(s.Balance-wantBal) > 0.005 {
		t.Errorf("after storno: balance %.2f, want %.2f (Guthaben of the reversed USt %.2f)", s.Balance, wantBal, iv.TaxAmount)
	}
}

// setStatus posts a vehicle status change (optional back-dated date).
func setStatus(t *testing.T, h *Handler, vid int64, status, date string) {
	t.Helper()
	payload := map[string]any{"status": status}
	if date != "" {
		payload["date"] = date
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/status", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ChangeVehicleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %s: %d %s", status, rec.Code, rec.Body.String())
	}
}

// TestListChargesRejectsBadPersonID (review: ListCharges): a non-numeric
// ?person_id must yield 400, not a 500 from a failed SQL int cast.
func TestListChargesRejectsBadPersonID(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/charges?person_id=abc", nil)
	rec := httptest.NewRecorder()
	h.ListCharges(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad person_id must be 400, got %d %s", rec.Code, rec.Body.String())
	}
}

// updateVehicleFields PUTs a vehicle built from its current state plus overrides.
func updateVehicleFields(t *testing.T, h *Handler, pid, vid int64, override map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	lrec := httptest.NewRecorder()
	h.ListVehicles(lrec, httptest.NewRequest(http.MethodGet, "/api/vehicles?person_id="+strconv.FormatInt(pid, 10), nil))
	var vehs []models.Vehicle
	_ = json.Unmarshal(lrec.Body.Bytes(), &vehs)
	var v models.Vehicle
	for _, x := range vehs {
		if x.ID == vid {
			v = x
		}
	}
	body := map[string]any{
		"person_id": v.PersonID, "category_id": v.CategoryID, "billing_period": v.BillingPeriod,
		"status": v.Status, "start_date": v.StartDate.Format("2006-01-02"), "rate": v.Rate,
	}
	for k, val := range override {
		body[k] = val
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/vehicles/"+strconv.FormatInt(vid, 10), bytes.NewReader(b))
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.UpdateVehicle(rec, req)
	return rec
}

// TestVehicleEndDateRetractBlocked (review B3): setting end_date before the end of
// an already-invoiced period must be blocked (that would drop the period from
// accrual while its payment stays → phantom Guthaben); extending must still work.
func TestVehicleEndDateRetractBlocked(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(2).Format("2006-01-02"))
	createInvoice(t, h, pid) // locks the two completed months

	// Retract end_date into an invoiced month → 409.
	if rec := updateVehicleFields(t, h, pid, vid, map[string]any{"end_date": firstOfMonthMonthsAgo(1).Format("2006-01-02")}); rec.Code != http.StatusConflict {
		t.Errorf("retracting end_date below an invoiced period must be 409, got %d %s", rec.Code, rec.Body.String())
	}
	// Extend (close in the future) → allowed.
	if rec := updateVehicleFields(t, h, pid, vid, map[string]any{"end_date": firstOfMonthMonthsAgo(-6).Format("2006-01-02")}); rec.Code != http.StatusOK {
		t.Errorf("extending end_date on an invoiced vehicle must be allowed, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestAgreementMembershipFrozenWhenInvoiced (review B2): changing the covered
// vehicle set of an invoiced Pauschale must be blocked — an unbound vehicle would
// re-bill its period individually, a newly-covered one would phantom-credit an
// already-invoiced period.
func TestAgreementMembershipFrozenWhenInvoiced(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	start := firstOfMonthMonthsAgo(2).Format("2006-01-02")
	aid := mkAgreement(t, h, pid, 100, "monthly", start)
	vid := mkStoredVehicle(t, h, pid, 30, start)
	createInvoice(t, h, pid) // locks the agreement's completed periods

	ubody, _ := json.Marshal(map[string]any{
		"amount": 100, "period": "monthly", "start_date": start, "vehicle_ids": []int64{vid},
	})
	ureq := httptest.NewRequest(http.MethodPut, "/api/agreements/"+strconv.FormatInt(aid, 10), bytes.NewReader(ubody))
	ureq.SetPathValue("id", strconv.FormatInt(aid, 10))
	ureq.Header.Set("Content-Type", "application/json")
	urec := httptest.NewRecorder()
	h.UpdateAgreement(urec, ureq)
	if urec.Code != http.StatusConflict {
		t.Errorf("changing covered vehicles of an invoiced agreement must be 409, got %d %s", urec.Code, urec.Body.String())
	}
}

// TestReconcileMonthsToYear (S1): monthly bars must sum exactly to the year total
// after the largest-remainder reconcile (per-month rounding of a yearly item
// drifts a cent or two on its own).
func TestReconcileMonthsToYear(t *testing.T) {
	months := []float64{84.93, 76.71, 84.93, 82.19, 84.93, 82.19, 84.93, 84.93, 82.19, 84.93, 82.19, 84.93}
	reconcileMonthsToYear(months, 1000.00) // raw sum 999.98 → +0.02 distributed
	var sum float64
	for _, m := range months {
		sum += m
	}
	if math.Abs(sum-1000.00) > 0.005 {
		t.Errorf("months must sum to the year total 1000.00, got %.2f", sum)
	}
}

// TestBackdatedCollectBelowInvoicedBlocked (regression): a back-dated collect via
// the status endpoint must not set end_date below an invoiced period (phantom
// Guthaben) — same guard as UpdateVehicle.
func TestBackdatedCollectBelowInvoicedBlocked(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(2).Format("2006-01-02"))
	createInvoice(t, h, pid) // locks the two completed months

	// Collect back-dated into an invoiced month → 409.
	body, _ := json.Marshal(map[string]any{"status": "collected", "date": firstOfMonthMonthsAgo(1).Format("2006-01-02")})
	req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/status", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ChangeVehicleStatus(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("back-dated collect below an invoiced period must be 409, got %d %s", rec.Code, rec.Body.String())
	}
}

// getVehicleT returns a person's vehicle (incl. archived) from ListVehicles.
func getVehicleT(t *testing.T, h *Handler, pid, vid int64) models.Vehicle {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ListVehicles(rec, httptest.NewRequest(http.MethodGet, "/api/vehicles?person_id="+strconv.FormatInt(pid, 10), nil))
	var vehs []models.Vehicle
	if err := json.Unmarshal(rec.Body.Bytes(), &vehs); err != nil {
		t.Fatalf("decode vehicles: %v", err)
	}
	for _, x := range vehs {
		if x.ID == vid {
			return x
		}
	}
	t.Fatalf("vehicle %d not found", vid)
	return models.Vehicle{}
}

// TestInvoicedVehicleBadgeAndArchive (3-part fix): a collected vehicle billed on
// an invoice shows invoiced/invoice_open, and once the invoice is fully paid it
// flips to invoiced+!open and is auto-archived (balance 0).
func TestInvoicedVehicleBadgeAndArchive(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))
	// Collect with a PAST end date so every period has elapsed → fully invoiceable.
	setStatus(t, h, vid, "collected", firstOfMonthMonthsAgo(1).Format("2006-01-02"))
	iv := createInvoice(t, h, pid)

	if v := getVehicleT(t, h, pid, vid); !v.Invoiced || !v.InvoiceOpen || v.Archived {
		t.Fatalf("after invoicing: invoiced=%v open=%v archived=%v (want true,true,false)", v.Invoiced, v.InvoiceOpen, v.Archived)
	}

	payInvoices(t, h, pid, map[string]any{"amount": iv.Total, "method": "bar", "auto": true})

	if v := getVehicleT(t, h, pid, vid); !v.Invoiced || v.InvoiceOpen || !v.Archived {
		t.Errorf("after paying the invoice: invoiced=%v open=%v archived=%v (want true,false,true)", v.Invoiced, v.InvoiceOpen, v.Archived)
	}
	if b := personStatsT(t, h, pid).Balance; math.Abs(b) > 0.005 {
		t.Errorf("balance after full payment must be 0, got %.2f", b)
	}
}
