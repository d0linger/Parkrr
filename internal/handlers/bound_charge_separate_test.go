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

	"github.com/preining/parkrr/internal/models"
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

// TestBoundChargeSettledByVehicleSlider (#B, updated): the vehicle's "bezahlt"
// slider now settles its bound Zusatzkosten too — one auto-payment covers rent +
// extras — so the extra is collected (not dropped, not left owed). Previously the
// slider settled only rent and the extra stayed owed while showing "bezahlt".
func TestBoundChargeSettledByVehicleSlider(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(2).Format("2006-01-02"))

	// A bound one-off extra.
	chBody, _ := json.Marshal(map[string]any{
		"person_id": pid, "vehicle_id": vid, "description": "Sonderreinigung", "amount": 60, "quantity": 1,
	})
	chrec := httptest.NewRecorder()
	h.CreateCharge(chrec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(chBody)))
	if chrec.Code != http.StatusCreated {
		t.Fatalf("bound charge: %d %s", chrec.Code, chrec.Body.String())
	}
	before := personStatsT(t, h, pid).Balance

	// Mark the VEHICLE paid → its auto-payment now covers rent AND the bound extra.
	pbody, _ := json.Marshal(map[string]any{"paid": true})
	preq := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/paid", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(vid, 10))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	h.MarkPaid(prec, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("markpaid: %d %s", prec.Code, prec.Body.String())
	}

	// Balance nets to 0 (rent + the 60 extra covered) and the recorded payment
	// includes the extra — the extra is collected, not left outstanding.
	s := personStatsT(t, h, pid)
	if math.Abs(s.Balance) > 0.005 {
		t.Errorf("after paying the vehicle, balance must be 0 (rent + extra covered), got %.2f (before %.2f)", s.Balance, before)
	}
	if s.PaymentsTotal+0.005 < 60 {
		t.Errorf("the auto-payment must include the bound extra (>=60), got %.2f", s.PaymentsTotal)
	}
}

// listChargesT returns a person's charges via the ListCharges endpoint.
func listChargesT(t *testing.T, h *Handler, pid int64) []models.Charge {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/charges?person_id="+strconv.FormatInt(pid, 10), nil)
	rec := httptest.NewRecorder()
	h.ListCharges(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list charges: %d %s", rec.Code, rec.Body.String())
	}
	var out []models.Charge
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

// TestBoundChargeInvoiceStatusSettledWhenInvoicePaid (Fix 3): a bound Zusatzkosten
// billed on a Rechnung shows "fakturiert" while the invoice is open and flips to
// "bezahlt · Rechnung" once the invoice is fully paid — PayInvoices settles the
// invoice, not the raw charge flag, so the status is derived from the covering
// invoice (mirroring the vehicle). Before the fix it lingered as "offen" forever.
func TestBoundChargeInvoiceStatusSettledWhenInvoicePaid(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(2).Format("2006-01-02"))

	// A bound one-off extra, billed alongside the rent.
	chBody, _ := json.Marshal(map[string]any{
		"person_id": pid, "vehicle_id": vid, "description": "Innenreinigung", "amount": 60, "quantity": 1,
	})
	chrec := httptest.NewRecorder()
	h.CreateCharge(chrec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(chBody)))
	if chrec.Code != http.StatusCreated {
		t.Fatalf("bound charge: %d %s", chrec.Code, chrec.Body.String())
	}

	iv := createInvoice(t, h, pid) // bills the rent + the 60€ extra

	// While the invoice is open: the bound charge is "fakturiert" (invoiced, open),
	// not settled.
	boundOf := func() models.Charge {
		for _, c := range listChargesT(t, h, pid) {
			if c.VehicleID != nil {
				return c
			}
		}
		t.Fatalf("bound charge not found in list")
		return models.Charge{}
	}
	if c := boundOf(); !c.Invoiced || !c.InvoiceOpen {
		t.Errorf("before payment the bound charge must be invoiced+open (fakturiert), got invoiced=%v open=%v", c.Invoiced, c.InvoiceOpen)
	}

	// Pay the invoice in full.
	full := getInvoiceT(t, h, iv.ID)
	payInvoices(t, h, pid, map[string]any{"amount": full.Total, "auto": true})

	// Now the bound charge is settled through the paid Rechnung: invoiced, not open.
	if c := boundOf(); !c.Invoiced || c.InvoiceOpen {
		t.Errorf("after paying the invoice the bound charge must read bezahlt·Rechnung (invoiced, not open), got invoiced=%v open=%v", c.Invoiced, c.InvoiceOpen)
	}
}

// TestBoundRecurringBilledDespitePauschale (#A): a vehicle-bound recurring
// Nebenkosten on a PAID-Pauschale-covered vehicle must be billed — the flat rate
// covers only rent, not the recurring extra. Before the fix the covering Pauschale
// absorbed it, leaving it owed in the balance but never invoiceable.
func TestBoundRecurringBilledDespitePauschale(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	// A PAID Pauschale fully covering the vehicle (rent settled).
	agBody, _ := json.Marshal(map[string]any{
		"amount": 30, "period": "monthly", "start_date": firstOfMonthMonthsAgo(3).Format("2006-01-02"),
		"vehicle_ids": []int64{vid}, "paid": true,
	})
	arec := httptest.NewRecorder()
	areq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/agreements", bytes.NewReader(agBody))
	areq.SetPathValue("id", strconv.FormatInt(pid, 10))
	h.CreateAgreement(arec, areq)
	if arec.Code != http.StatusOK && arec.Code != http.StatusCreated {
		t.Fatalf("agreement: %d %s", arec.Code, arec.Body.String())
	}

	// A recurring Nebenkosten bound to that covered vehicle: 20/month.
	amt := 20.0
	rcBody, _ := json.Marshal(recurringRequest{
		Description: "Versicherung", Amount: &amt, Period: "monthly",
		StartDate: firstOfMonthMonthsAgo(3).Format("2006-01-02"), VehicleID: &vid,
	})
	rrec := httptest.NewRecorder()
	rreq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/recurring", bytes.NewReader(rcBody))
	rreq.SetPathValue("id", strconv.FormatInt(pid, 10))
	rreq.Header.Set("Content-Type", "application/json")
	h.CreateRecurringCharge(rrec, rreq)
	if rrec.Code != http.StatusOK && rrec.Code != http.StatusCreated {
		t.Fatalf("recurring: %d %s", rrec.Code, rrec.Body.String())
	}

	// Rent covered (Pauschale, paid) + Pauschale paid → invoice bills ONLY the
	// recurring extra: 3 completed months × 20 = 60.
	iv := createInvoice(t, h, pid)
	if math.Abs(iv.Subtotal-60) > 0.05 {
		full := getInvoiceT(t, h, iv.ID)
		t.Errorf("bound recurring must be billed despite the paid Pauschale: expected 60, got %.2f; items=%+v", iv.Subtotal, full.Items)
	}
}
