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

// TestBoundChargeBilledDespitePauschale (#4, Option A): a one-off Zusatzkosten
// bound to a vehicle that is fully covered by a PAID Pauschale must still be
// invoiced — the flat rate settles the base rent, never the extra. Before the fix
// the covering Pauschale silently absorbed (un-billed) the charge.
func TestBoundChargeBilledDespitePauschale(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)

	// Tariff + stored vehicle.
	cbody, _ := json.Marshal(map[string]any{
		"name": "SepTarif-" + strconv.FormatInt(time.Now().UnixNano(), 10), "default_monthly_cost": 30, "default_yearly_cost": 300,
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
		"status": "stored", "start_date": "2026-01-01", "label": "SepAuto",
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

	// A PAID Pauschale that fully covers the vehicle (base rent settled).
	agBody, _ := json.Marshal(map[string]any{
		"amount": 30, "period": "monthly", "start_date": "2026-01-01",
		"vehicle_ids": []int64{veh.ID}, "paid": true,
	})
	arec := httptest.NewRecorder()
	areq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/agreements", bytes.NewReader(agBody))
	areq.SetPathValue("id", strconv.FormatInt(pid, 10))
	h.CreateAgreement(arec, areq)
	if arec.Code != http.StatusOK && arec.Code != http.StatusCreated {
		t.Fatalf("agreement: %d %s", arec.Code, arec.Body.String())
	}

	// A one-off Zusatzkosten bound to the covered vehicle.
	chBody, _ := json.Marshal(map[string]any{
		"person_id": pid, "vehicle_id": veh.ID, "description": "Reifenwechsel", "amount": 60, "quantity": 1, "charged_on": "2026-05-01",
	})
	chrec := httptest.NewRecorder()
	h.CreateCharge(chrec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(chBody)))
	if chrec.Code != http.StatusCreated {
		t.Fatalf("bound charge: %d %s", chrec.Code, chrec.Body.String())
	}

	iv := createInvoice(t, h, pid)
	full := getInvoiceT(t, h, iv.ID)

	hasExtra := false
	for _, it := range full.Items {
		if it.LineTotal == 60 {
			hasExtra = true
		}
	}
	if !hasExtra {
		t.Errorf("bound Zusatzkosten (60) must be billed despite the paid Pauschale; items=%+v", full.Items)
	}
}
