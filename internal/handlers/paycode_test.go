package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestEPCPayload(t *testing.T) {
	p, problem := epcPayload("Parkrr e.U.", "AT61 1904 3002 3457 3201", "", 55.0, "2026-0001")
	if problem != "" {
		t.Fatalf("unexpected problem: %s", problem)
	}
	for _, want := range []string{"BCD", "002", "SCT", "AT611904300234573201", "EUR55.00", "2026-0001"} {
		if !strings.Contains(p, want) {
			t.Errorf("payload missing %q\n---\n%s", want, p)
		}
	}
	if !strings.HasPrefix(p, "BCD\n") {
		t.Error("payload must start with the BCD service tag")
	}
	if _, prob := epcPayload("X", "", "", 5, "ref"); prob == "" {
		t.Error("empty IBAN must yield a problem string")
	}
}

// Runs only when PARKRR_TEST_DATABASE_URL is set.
func TestPayQR(t *testing.T) {
	h := testHandler(t)
	saveBilling(t, h, map[string]any{
		"seller_name": "Parkrr e.U.", "seller_address": "Musterstr. 1, 1010 Wien",
		"kleinunternehmer": true, "number_pad": 4, "iban": "AT611904300234573201",
	})
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 55.0)
	iv := createInvoice(t, h, pid)

	req := httptest.NewRequest(http.MethodGet, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/pay-qr", nil)
	req.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	w := httptest.NewRecorder()
	h.PayQR(w, req)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("pay-qr: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG")) {
		t.Error("pay-qr body is not a PNG")
	}

	// A fully-paid invoice has nothing to pay → 409 (exercised via the shared helper).
	pw := httptest.NewRecorder()
	h.serveInvoicePayQR(pw, invoice{OpenAmount: 0, Total: 10, Number: "PAID-1", Seller: map[string]any{"iban": "AT611904300234573201"}})
	if pw.Code != http.StatusConflict {
		t.Errorf("paid invoice pay-qr: want 409, got %d", pw.Code)
	}
}
