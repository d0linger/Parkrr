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

// TestPaymentItemVehicleFallbackToCategory: a Gefährt with no label and no plate is
// named by its category everywhere (like the vehicle card) — the payment attribution
// must do the same, not fall back to the generic "Gefährt".
func TestPaymentItemVehicleFallbackToCategory(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	catName := "PKW (Halle)-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cbody, _ := json.Marshal(map[string]any{"name": catName, "default_monthly_cost": 30, "default_yearly_cost": 300})
	crec := httptest.NewRecorder()
	h.CreateCategory(crec, httptest.NewRequest(http.MethodPost, "/api/categories", bytes.NewReader(cbody)))
	var cat struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(crec.Body.Bytes(), &cat)

	vbody, _ := json.Marshal(map[string]any{"person_id": pid, "category_id": cat.ID, "billing_period": "monthly",
		"status": "stored", "start_date": firstOfMonthMonthsAgo(1).Format("2006-01-02")}) // no label, no plate
	vrec := httptest.NewRecorder()
	h.CreateVehicle(vrec, httptest.NewRequest(http.MethodPost, "/api/vehicles", bytes.NewReader(vbody)))
	if vrec.Code != http.StatusCreated {
		t.Fatalf("vehicle: %d %s", vrec.Code, vrec.Body.String())
	}
	var veh struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(vrec.Body.Bytes(), &veh)

	pbody, _ := json.Marshal(map[string]any{"paid": true})
	preq := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(veh.ID, 10)+"/paid", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(veh.ID, 10))
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
	var vehLabel string
	for _, list := range items {
		for _, it := range list {
			if it.Kind == "vehicle" {
				vehLabel = it.Label
			}
		}
	}
	if vehLabel != catName {
		t.Errorf("a label-less Gefährt must be named by its category (%q), got %q", catName, vehLabel)
	}
}

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
