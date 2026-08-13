package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// A canceled (storniert) invoice must not be payable: no SEPA pay-QR, no
// reminder e-mail, and its amount must not count toward the portal open total.
// Regression for the cloud-review "canceled invoices still payable" finding.
func TestCanceledInvoiceNotPayable(t *testing.T) {
	h := testHandler(t)
	saveBilling(t, h, map[string]any{
		"seller_name": "Parkrr e.U.", "seller_address": "Musterstr. 1, 1010 Wien",
		"kleinunternehmer": true, "number_pad": 4, "iban": "AT611904300234573201",
	})
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 30.0)
	iv := createInvoice(t, h, pid)
	ivs := strconv.FormatInt(iv.ID, 10)

	// Cancel it.
	creq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+ivs+"/cancel", nil)
	creq.SetPathValue("id", ivs)
	crec := httptest.NewRecorder()
	h.CancelInvoice(crec, creq)
	if crec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", crec.Code, crec.Body.String())
	}

	// pay-QR → 409.
	qreq := httptest.NewRequest(http.MethodGet, "/api/invoices/"+ivs+"/pay-qr", nil)
	qreq.SetPathValue("id", ivs)
	qw := httptest.NewRecorder()
	h.PayQR(qw, qreq)
	if qw.Code != http.StatusConflict {
		t.Errorf("pay-qr on canceled: want 409, got %d", qw.Code)
	}

	// reminder → 409, nothing sent.
	fake := &captureSender{enabled: true}
	h.Mail = fake
	rreq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+ivs+"/remind", nil)
	rreq.SetPathValue("id", ivs)
	rw := httptest.NewRecorder()
	h.RemindInvoice(rw, rreq)
	if rw.Code != http.StatusConflict {
		t.Errorf("remind on canceled: want 409, got %d", rw.Code)
	}
	if fake.calls != 0 {
		t.Errorf("no e-mail must be sent for a canceled invoice; calls=%d", fake.calls)
	}

	// Portal summary: the canceled invoice contributes 0 to the open total.
	token := createPortalLink(t, h, pid)
	sw := getPortalSummary(t, h, token)
	if sw.Code != http.StatusOK {
		t.Fatalf("portal summary: %d %s", sw.Code, sw.Body.String())
	}
	var sum portalSummary
	if err := json.Unmarshal(sw.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decode portal summary: %v", err)
	}
	if sum.OpenTotal > 0.005 {
		t.Errorf("canceled invoice must not inflate open_total, got %v", sum.OpenTotal)
	}
	for _, inv := range sum.Invoices {
		if inv.ID == iv.ID && inv.Open > 0.005 {
			t.Errorf("canceled invoice open = %v, want 0", inv.Open)
		}
	}
}
