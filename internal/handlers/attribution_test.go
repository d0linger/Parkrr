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

// TestResolvePaymentItems: a vehicle's "bezahlt" slider records one auto-payment that
// settles the rent AND its bound Zusatzkosten — the resolved items must attribute it
// to the Gefährt and the charge, so the overview can show what the payment covered.
func TestResolvePaymentItems(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(2).Format("2006-01-02"))

	chBody, _ := json.Marshal(map[string]any{"person_id": pid, "vehicle_id": vid, "description": "Innenreinigung", "amount": 60, "quantity": 1})
	crec := httptest.NewRecorder()
	h.CreateCharge(crec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(chBody)))
	if crec.Code != http.StatusCreated {
		t.Fatalf("charge: %d %s", crec.Code, crec.Body.String())
	}

	pbody, _ := json.Marshal(map[string]any{"paid": true})
	preq := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/paid", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(vid, 10))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	h.MarkPaid(prec, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("markpaid: %d %s", prec.Code, prec.Body.String())
	}

	items, err := h.resolvePaymentItems(t.Context(), pid)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected exactly one payment with items, got %d", len(items))
	}
	var hasVehicle, hasCharge bool
	var chargeAmt float64
	for _, list := range items {
		for _, it := range list {
			switch it.Kind {
			case "vehicle":
				hasVehicle = true
			case "charge":
				hasCharge = true
				chargeAmt = it.Amount
			}
		}
	}
	if !hasVehicle || !hasCharge {
		t.Errorf("payment items must attribute the Gefährt and the Zusatzkosten; got %+v", items)
	}
	if math.Abs(chargeAmt-60) > 0.005 {
		t.Errorf("the Zusatzkosten item amount should be 60, got %.2f", chargeAmt)
	}
}

// TestResolveInvoiceItems: an invoiced Pauschale must surface as a structured
// position (kind=agreement + period) for the invoice-row summary.
func TestResolveInvoiceItems(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	_ = mkAgreement(t, h, pid, 30, "monthly", firstOfMonthMonthsAgo(3).Format("2006-01-02"))
	createInvoice(t, h, pid) // bills the completed Pauschale periods

	pos, err := h.resolveInvoiceItems(t.Context(), pid)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	found := false
	for _, list := range pos {
		for _, it := range list {
			if it.Kind == "agreement" && it.Period != "" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("invoice positions should include the Pauschale (agreement) with a period; got %+v", pos)
	}
}
