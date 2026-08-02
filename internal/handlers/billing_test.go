package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
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
