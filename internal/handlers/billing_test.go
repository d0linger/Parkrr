package handlers

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func chargeFor(t *testing.T, h *Handler, pid int64, amount float64) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"person_id": pid, "description": "Pos", "amount": amount, "quantity": 1, "charged_on": "2026-05-01",
	})
	rec := httptest.NewRecorder()
	h.CreateCharge(rec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("charge: %d %s", rec.Code, rec.Body.String())
	}
}

func createInvoice(t *testing.T, h *Handler, pid int64) invoice {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateInvoice(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invoice: %d %s", rec.Code, rec.Body.String())
	}
	var iv invoice
	if err := json.Unmarshal(rec.Body.Bytes(), &iv); err != nil {
		t.Fatalf("decode invoice: %v", err)
	}
	return iv
}

func saveBilling(t *testing.T, h *Handler, payload map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/billing/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SaveBillingSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save billing: %d %s", rec.Code, rec.Body.String())
	}
}

// compliantSeller sets the §11-UStG mandatory seller fields so invoices are issuable.
func compliantSeller(t *testing.T, h *Handler) {
	t.Helper()
	saveBilling(t, h, map[string]any{
		"seller_name": "Parkrr e.U.", "seller_address": "Musterstr. 1, 1010 Wien",
		"kleinunternehmer": true, "number_pad": 4,
	})
}

func TestInvoiceKleinunternehmerAndUSt(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)

	// Kleinunternehmer (default): no USt, total == sum of lines.
	p1 := createIntegrationPerson(t, h)
	chargeFor(t, h, p1, 100)
	iv := createInvoice(t, h, p1)
	if iv.Number == "" {
		t.Error("expected a non-empty invoice number")
	}
	if !iv.Kleinunternehmer || iv.TaxAmount != 0 || iv.Total != 100 || iv.Subtotal != 100 {
		t.Errorf("kleinunternehmer invoice wrong: %+v", iv)
	}

	// Switch to USt 20 %: tax on top of the net line totals.
	saveBilling(t, h, map[string]any{
		"seller_name": "Test GmbH", "seller_address": "Musterstr. 1, 1010 Wien", "seller_uid": "ATU12345678",
		"kleinunternehmer": false, "ust_rate": 20, "number_pad": 4,
	})
	p2 := createIntegrationPerson(t, h)
	chargeFor(t, h, p2, 100)
	iv2 := createInvoice(t, h, p2)
	if iv2.Kleinunternehmer {
		t.Error("expected USt invoice")
	}
	if iv2.Subtotal != 100 || iv2.TaxAmount != 20 || iv2.Total != 120 || iv2.UStRate != 20 {
		t.Errorf("USt invoice math wrong: %+v", iv2)
	}
	if iv2.Number == iv.Number {
		t.Error("invoice numbers must be unique/sequential")
	}

	// GetInvoice returns the line items + snapshots.
	greq := httptest.NewRequest(http.MethodGet, "/api/invoices/"+strconv.FormatInt(iv2.ID, 10), nil)
	greq.SetPathValue("id", strconv.FormatInt(iv2.ID, 10))
	grec := httptest.NewRecorder()
	h.GetInvoice(grec, greq)
	if grec.Code != http.StatusOK {
		t.Fatalf("get invoice: %d", grec.Code)
	}
	var full invoice
	_ = json.Unmarshal(grec.Body.Bytes(), &full)
	if len(full.Items) != 1 || full.Items[0].LineTotal != 100 {
		t.Errorf("invoice items wrong: %+v", full.Items)
	}
	if full.Seller["uid"] != "ATU12345678" {
		t.Errorf("seller snapshot missing UID: %+v", full.Seller)
	}
}

// TestInvoiceTotalMatchesBalanceWithBoundCharge proves the invoice bills the FULL
// open picture (incl. a vehicle-bound Zusatzkosten) — its total equals the
// person's open balance, not just the standalone items.
func TestInvoiceTotalMatchesBalanceWithBoundCharge(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)

	// A tariff (unique name so repeated runs don't collide) + a stored vehicle.
	cbody, _ := json.Marshal(map[string]any{"name": "IntegTarif-" + strconv.FormatInt(time.Now().UnixNano(), 10), "default_monthly_cost": 30, "default_yearly_cost": 300})
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
		"status": "stored", "start_date": "2026-01-01", "label": "IntegAuto",
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

	// ... plus a Zusatzkosten BOUND to that vehicle.
	chbody, _ := json.Marshal(map[string]any{
		"person_id": pid, "vehicle_id": veh.ID, "description": "Bound extra", "amount": 60, "quantity": 1, "charged_on": "2026-05-01",
	})
	chrec := httptest.NewRecorder()
	h.CreateCharge(chrec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(chbody)))
	if chrec.Code != http.StatusCreated {
		t.Fatalf("bound charge: %d %s", chrec.Code, chrec.Body.String())
	}

	// Person's open balance.
	sreq := httptest.NewRequest(http.MethodGet, "/api/persons/"+strconv.FormatInt(pid, 10)+"/stats", nil)
	sreq.SetPathValue("id", strconv.FormatInt(pid, 10))
	srec := httptest.NewRecorder()
	h.PersonStats(srec, sreq)
	var st struct {
		Balance float64 `json:"balance"`
	}
	_ = json.Unmarshal(srec.Body.Bytes(), &st)

	iv := createInvoice(t, h, pid)
	// Invoice must include the bound charge line and its total must equal balance.
	full := httptest.NewRequest(http.MethodGet, "/api/invoices/"+strconv.FormatInt(iv.ID, 10), nil)
	full.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	frec := httptest.NewRecorder()
	h.GetInvoice(frec, full)
	var fiv invoice
	_ = json.Unmarshal(frec.Body.Bytes(), &fiv)

	hasBound := false
	for _, it := range fiv.Items {
		if it.LineTotal == 60 {
			hasBound = true
		}
	}
	if !hasBound {
		t.Errorf("invoice must include the 60 € bound charge; items: %+v", fiv.Items)
	}
	// Vehicle rent is now billed per COMPLETED period (B1-Rest / #3), so the
	// invoice covers the discrete bound charge in full plus every completed month —
	// everything except the still-running current month. The open balance additionally
	// carries that running month's accrual, so balance − subtotal is exactly the
	// current month's rent: positive and below one whole month (30 €).
	diff := st.Balance - iv.Subtotal
	if diff < -0.05 || diff > 30.05 {
		t.Errorf("balance %.2f minus subtotal %.2f must equal only the running month's rent (0..30), got %.2f",
			st.Balance, iv.Subtotal, diff)
	}
}

func TestInvoiceRejectsNoOpenPositions(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateInvoice(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for no open positions, got %d", rec.Code)
	}
}

func issueInvoiceRec(t *testing.T, h *Handler, pid int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateInvoice(rec, req)
	return rec
}

// TC#4 — §11/§14 UStG: an invoice must not be issued when a mandatory field is
// missing (here: the seller name/address), and must not burn a number.
func TestInvoiceComplianceRefusal(t *testing.T) {
	h := testHandler(t)
	// Deliberately reset seller data to blank (Kleinunternehmer, no name/address).
	if _, err := h.Pool.Exec(t.Context(), `UPDATE billing_settings SET seller_name='', seller_address='' WHERE id=1`); err != nil {
		t.Fatalf("reset settings: %v", err)
	}
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)

	var noBefore int
	_ = h.Pool.QueryRow(t.Context(), `SELECT next_invoice_no FROM billing_settings WHERE id=1`).Scan(&noBefore)

	rec := issueInvoiceRec(t, h, pid)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing §11 fields, got %d %s", rec.Code, rec.Body.String())
	}
	var noAfter int
	_ = h.Pool.QueryRow(t.Context(), `SELECT next_invoice_no FROM billing_settings WHERE id=1`).Scan(&noAfter)
	if noAfter != noBefore {
		t.Errorf("a refused invoice must not consume a number (%d -> %d)", noBefore, noAfter)
	}
}

// TC#1/B1 — Fakturier-Sperre: the same position can't be invoiced twice.
func TestNoDoubleInvoicing(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)

	first := createInvoice(t, h, pid) // 201, locks the charge
	if first.Total != 100 {
		t.Fatalf("first invoice total %v", first.Total)
	}
	// Second run: the charge is locked -> no open positions -> 400.
	rec := issueInvoiceRec(t, h, pid)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (nothing left to invoice), got %d %s", rec.Code, rec.Body.String())
	}
}

// TC — Storno/Unveränderbarkeit: cancel creates a negated counter-document,
// marks the original canceled, releases the position so it can be re-invoiced,
// and a Storno can't itself be canceled.
func TestInvoiceStorno(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	orig := createInvoice(t, h, pid)

	// Cancel it.
	creq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(orig.ID, 10)+"/cancel", nil)
	creq.SetPathValue("id", strconv.FormatInt(orig.ID, 10))
	crec := httptest.NewRecorder()
	h.CancelInvoice(crec, creq)
	if crec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", crec.Code, crec.Body.String())
	}
	var storno invoice
	_ = json.Unmarshal(crec.Body.Bytes(), &storno)
	if storno.Total != -100 || storno.CancelsID == nil || *storno.CancelsID != orig.ID {
		t.Fatalf("storno document wrong: %+v", storno)
	}

	// Original is now canceled, and the position is released -> re-invoiceable.
	var canceled bool
	_ = h.Pool.QueryRow(t.Context(), `SELECT canceled FROM invoices WHERE id=$1`, orig.ID).Scan(&canceled)
	if !canceled {
		t.Error("original invoice must be marked canceled")
	}
	again := createInvoice(t, h, pid) // works again because the charge was released
	if again.Total != 100 {
		t.Errorf("released position should be re-invoiceable, got %+v", again)
	}

	// A Storno document cannot itself be canceled.
	c2 := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(storno.ID, 10)+"/cancel", nil)
	c2.SetPathValue("id", strconv.FormatInt(storno.ID, 10))
	c2rec := httptest.NewRecorder()
	h.CancelInvoice(c2rec, c2)
	if c2rec.Code != http.StatusBadRequest {
		t.Errorf("canceling a Storno must be rejected (400), got %d", c2rec.Code)
	}
}

// --- P2: pay invoices (OP) ---

type payResult struct {
	PaymentID   int64   `json:"payment_id"`
	Allocated   float64 `json:"allocated"`
	Unallocated float64 `json:"unallocated"`
	Invoices    int     `json:"invoices_settled"`
}

func payInvoicesRec(t *testing.T, h *Handler, pid int64, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/pay-invoices", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.PayInvoices(rec, req)
	return rec
}
func payInvoices(t *testing.T, h *Handler, pid int64, payload map[string]any) payResult {
	t.Helper()
	rec := payInvoicesRec(t, h, pid, payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pay-invoices: %d %s", rec.Code, rec.Body.String())
	}
	var r payResult
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	return r
}
func getInvoiceT(t *testing.T, h *Handler, id int64) invoice {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/invoices/"+strconv.FormatInt(id, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	h.GetInvoice(rec, req)
	var iv invoice
	_ = json.Unmarshal(rec.Body.Bytes(), &iv)
	return iv
}

// TestPayInvoiceLifecycle: partial -> teilbezahlt, full -> bezahlt, overpay ->
// Guthaben; open_amount / invoiced_open track it; and Guthaben stays payments−accrued.
func TestPayInvoiceLifecycle(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid) // total 100

	r1 := payInvoices(t, h, pid, map[string]any{"amount": 40, "method": "ueberweisung", "auto": true})
	if r1.Allocated != 40 || r1.Unallocated != 0 {
		t.Fatalf("partial payment result wrong: %+v", r1)
	}
	iv1 := getInvoiceT(t, h, iv.ID)
	if iv1.Status != "teilbezahlt" || iv1.OpenAmount != 60 || iv1.PaidAmount != 40 {
		t.Errorf("teilbezahlt state wrong: %+v", iv1)
	}

	// Overpay: 100 more -> invoice fully paid (caps at 60 open), 40 stays as Guthaben.
	r2 := payInvoices(t, h, pid, map[string]any{"amount": 100, "method": "bar", "auto": true})
	if r2.Allocated != 60 || r2.Unallocated != 40 {
		t.Fatalf("overpay result wrong: %+v", r2)
	}
	iv2 := getInvoiceT(t, h, iv.ID)
	if iv2.Status != "bezahlt" || iv2.OpenAmount != 0 {
		t.Errorf("bezahlt state wrong: %+v", iv2)
	}
}

// personStatsT fetches a person's stats response for balance/credit assertions.
func personStatsT(t *testing.T, h *Handler, pid int64) personStatsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/persons/"+strconv.FormatInt(pid, 10)+"/stats", nil)
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	rec := httptest.NewRecorder()
	h.PersonStats(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
	}
	var s personStatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	return s
}

// TestPaidUStInvoiceNoPhantomCredit is the regression for the net-vs-gross seam:
// accrual is NET, but an issued USt invoice adds tax on top and the customer pays
// GROSS. The invoiced tax must count on the owed side, otherwise fully paying a
// USt invoice mints Guthaben equal to the tax. Anna-Bauer bug, tax variant.
func TestPaidUStInvoiceNoPhantomCredit(t *testing.T) {
	h := testHandler(t)
	saveBilling(t, h, map[string]any{
		"seller_name": "Test GmbH", "seller_address": "Musterstr. 1, 1010 Wien", "seller_uid": "ATU12345678",
		"kleinunternehmer": false, "ust_rate": 20, "number_pad": 4,
	})
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100) // net 100
	iv := createInvoice(t, h, pid)
	if iv.TaxAmount != 20 || iv.Total != 120 {
		t.Fatalf("expected gross 120 (tax 20), got %+v", iv)
	}
	// Owed side must now be the gross 120 (net 100 + invoiced tax 20), no credit.
	if s := personStatsT(t, h, pid); math.Abs(s.Balance-120) > 0.05 || s.Credit > 0.05 {
		t.Fatalf("after USt invoice: balance=%.2f credit=%.2f (want 120 / 0)", s.Balance, s.Credit)
	}
	// Pay the gross in full -> balance 0, NO phantom Guthaben equal to the tax.
	payInvoices(t, h, pid, map[string]any{"amount": 120, "method": "ueberweisung", "auto": true})
	s := personStatsT(t, h, pid)
	if math.Abs(s.Balance) > 0.05 || s.Credit > 0.05 {
		t.Errorf("paid USt invoice must leave balance 0 / credit 0, got balance=%.2f credit=%.2f", s.Balance, s.Credit)
	}
}

// TestSliderInertOnInvoicedPosition is the regression for the double-pay seam: an
// invoiced position is locked out of openOwedItems, so flipping its "bezahlt"
// slider must NOT mint a second (auto) payment on top of the open invoice.
func TestSliderInertOnInvoicedPosition(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "Standalone", 100, "2026-05-01")
	iv := createInvoice(t, h, pid) // locks the charge

	before := personStatsT(t, h, pid)
	setChargePaidT(t, h, cid, true) // slider on the now-invoiced charge
	after := personStatsT(t, h, pid)

	// No auto-payment => balance unchanged, invoice still fully open.
	if math.Abs(after.PaymentsTotal-before.PaymentsTotal) > 0.005 {
		t.Errorf("slider must not record a payment on an invoiced position: %.2f -> %.2f",
			before.PaymentsTotal, after.PaymentsTotal)
	}
	if s := getInvoiceT(t, h, iv.ID).Status; s != "offen" {
		t.Errorf("invoice must stay open (settle via pay-invoices), got %s", s)
	}
}

// TestFractionalQuantityChargeNoPhantomCredit (audit Math-D1): a charge with a
// fractional quantity must round the SAME way in the balance as on the invoice, so
// paying the invoice in full leaves no phantom Guthaben.
func TestFractionalQuantityChargeNoPhantomCredit(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	// 33.33 × 1.5 = 49.995 → line rounds to 50.00 on the invoice AND in the balance.
	body, _ := json.Marshal(map[string]any{"person_id": pid, "description": "Stunden", "amount": 33.33, "quantity": 1.5, "charged_on": "2026-05-01"})
	rec := httptest.NewRecorder()
	h.CreateCharge(rec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("charge: %d %s", rec.Code, rec.Body.String())
	}
	iv := createInvoice(t, h, pid)
	payInvoices(t, h, pid, map[string]any{
		"amount": iv.Total, "method": "bar",
		"allocations": []map[string]any{{"invoice_id": iv.ID, "amount": iv.Total}},
	})
	if s := personStatsT(t, h, pid); s.Credit > 0.005 || math.Abs(s.Balance) > 0.005 {
		t.Errorf("fractional-qty charge paid in full must net to 0 with no phantom Guthaben, got balance=%.2f credit=%.2f", s.Balance, s.Credit)
	}
}

// TestPayInvoicesRespectsAutoAndZeroAlloc (#13): a pay-invoices call with auto off
// and no explicit selection books pure Guthaben (settles nothing); an explicit
// allocation of 0 to an invoice must not pay it in full.
func TestPayInvoicesRespectsAutoAndZeroAlloc(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid) // total 100, open

	// auto=false, no explicit allocations → pure Guthaben, invoice stays open.
	rec := payInvoicesRec(t, h, pid, map[string]any{"amount": 100, "method": "bar"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("pay: %d %s", rec.Code, rec.Body.String())
	}
	if s := getInvoiceT(t, h, iv.ID).Status; s != "offen" {
		t.Errorf("auto=false without selection must not settle the invoice, got status %s", s)
	}

	// Explicit allocation of 0 → must not pay the invoice.
	rec2 := payInvoicesRec(t, h, pid, map[string]any{
		"amount": 100, "method": "bar",
		"allocations": []map[string]any{{"invoice_id": iv.ID, "amount": 0}},
	})
	if rec2.Code != http.StatusCreated {
		t.Fatalf("pay2: %d %s", rec2.Code, rec2.Body.String())
	}
	if iv2 := getInvoiceT(t, h, iv.ID); iv2.Status != "offen" || iv2.PaidAmount > 0.005 {
		t.Errorf("zero allocation must not pay the invoice, got status=%s paid=%.2f", iv2.Status, iv2.PaidAmount)
	}
}

// TestPayInvoiceDeleteReverts (B3): deleting the payment restores the invoice
// atomically to open.
func TestPayInvoiceDeleteReverts(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)

	r := payInvoices(t, h, pid, map[string]any{"amount": 100, "method": "bar", "auto": true})
	if s := getInvoiceT(t, h, iv.ID).Status; s != "bezahlt" {
		t.Fatalf("expected bezahlt, got %s", s)
	}
	dreq := httptest.NewRequest(http.MethodDelete, "/api/payments/"+strconv.FormatInt(r.PaymentID, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(r.PaymentID, 10))
	drec := httptest.NewRecorder()
	h.DeletePayment(drec, dreq)
	if drec.Code != http.StatusOK {
		t.Fatalf("delete payment: %d %s", drec.Code, drec.Body.String())
	}
	back := getInvoiceT(t, h, iv.ID)
	if back.Status != "offen" || back.PaidAmount != 0 {
		t.Errorf("invoice must revert to open after payment delete: %+v", back)
	}
}

// TestPayCanceledInvoiceRejected: a canceled invoice can't be paid explicitly.
func TestPayCanceledInvoiceRejected(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)

	creq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/cancel", nil)
	creq.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	crec := httptest.NewRecorder()
	h.CancelInvoice(crec, creq)
	if crec.Code != http.StatusOK {
		t.Fatalf("cancel: %d", crec.Code)
	}
	rec := payInvoicesRec(t, h, pid, map[string]any{
		"amount": 100, "method": "bar",
		"allocations": []map[string]any{{"invoice_id": iv.ID, "amount": 100}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("paying a canceled invoice must be rejected (400), got %d %s", rec.Code, rec.Body.String())
	}
}

// TestOverdueInvoices (Mahnwesen light): an unpaid invoice past its due date
// appears in the overdue list and drops off once paid.
func TestOverdueInvoices(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)
	if _, err := h.Pool.Exec(t.Context(), `UPDATE invoices SET due_on = CURRENT_DATE - 5 WHERE id=$1`, iv.ID); err != nil {
		t.Fatalf("set due: %v", err)
	}
	list := func() *overdueInvoice {
		rec := httptest.NewRecorder()
		h.OverdueInvoices(rec, httptest.NewRequest(http.MethodGet, "/api/invoices/overdue", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("overdue: %d", rec.Code)
		}
		var o []overdueInvoice
		_ = json.Unmarshal(rec.Body.Bytes(), &o)
		for i := range o {
			if o[i].ID == iv.ID {
				return &o[i]
			}
		}
		return nil
	}
	got := list()
	if got == nil || got.DaysOverdue != 5 || got.OpenAmount != 100 {
		t.Fatalf("overdue invoice wrong: %+v", got)
	}
	payInvoices(t, h, pid, map[string]any{"amount": 100, "method": "bar", "auto": true})
	if list() != nil {
		t.Error("a paid invoice must drop off the overdue list")
	}
}
