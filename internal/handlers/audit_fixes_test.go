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

// TestCancelledVehicleStopsAccrual (audit W1): cancelling a vehicle must set an
// end_date so rent stops accruing — otherwise the archived vehicle grows an
// uncollectable receivable forever.
func TestCancelledVehicleStopsAccrual(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	// Cancel effective one month ago → accrual stops there (2 completed months = 60),
	// not the ~3 months it would keep growing to with a NULL end_date.
	cancelDate := firstOfMonthMonthsAgo(1).Format("2006-01-02")
	body, _ := json.Marshal(map[string]any{"status": "cancelled", "date": cancelDate})
	req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/status", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ChangeVehicleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	if b := personStatsT(t, h, pid).Balance; math.Abs(b-60) > 0.05 {
		t.Errorf("cancelled vehicle rent must stop at the end date (2 months = 60), got %.2f (unbounded growth?)", b)
	}
}

// TestDeletePersonWithPaymentBlocked (audit C2 / BAO §132): a person with a booked
// payment must not be deletable (the payments FK cascades — history would be lost).
func TestDeletePersonWithPaymentBlocked(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	pbody, _ := json.Marshal(map[string]any{"amount": 50, "method": "bar"})
	preq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/payments", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(pid, 10))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	h.CreatePayment(prec, preq)
	if prec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", prec.Code, prec.Body.String())
	}

	dreq := httptest.NewRequest(http.MethodDelete, "/api/persons/"+strconv.FormatInt(pid, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(pid, 10))
	drec := httptest.NewRecorder()
	h.DeletePerson(drec, dreq)
	if drec.Code != http.StatusConflict {
		t.Errorf("deleting a person with a booked payment must be blocked (409), got %d %s", drec.Code, drec.Body.String())
	}
}

// TestInvoiceHasLeistungszeitraum (audit C1 / §11 Abs 1 Z 4): an issued invoice
// must carry a service period (Leistungszeitraum) for the printed document.
func TestInvoiceHasLeistungszeitraum(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)
	full := getInvoiceT(t, h, iv.ID)
	if full.LeistungFrom == nil || full.LeistungTo == nil {
		t.Errorf("invoice must carry a Leistungszeitraum (§11 Abs 1 Z 4); from=%v to=%v", full.LeistungFrom, full.LeistungTo)
	}
}
