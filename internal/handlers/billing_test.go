package handlers

import (
	"bytes"
	"encoding/json"
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

func TestInvoiceKleinunternehmerAndUSt(t *testing.T) {
	h := testHandler(t)

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
		"seller_name": "Test GmbH", "seller_uid": "ATU12345678",
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
	// The line sum (subtotal) equals the open balance regardless of any USt added
	// on top. Tolerance covers cross-call timing (two time.Now() samples of the
	// continuous rent accrual) + per-line vs total rounding (0.05, as elsewhere).
	if diff := iv.Subtotal - st.Balance; diff > 0.05 || diff < -0.05 {
		t.Errorf("invoice line sum %.2f must equal open balance %.2f", iv.Subtotal, st.Balance)
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
