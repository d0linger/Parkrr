package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/models"
)

// TestStornoRevertsAgreementAndChargeSettlement (Storno-Gegenprobe): canceling the
// invoice that settled a Pauschale and its bound Zusatzkosten must make both revert
// to "offen" — the invoice-status derivations filter NOT canceled, so a Storno drops
// the settlement signal without any per-item change.
func TestStornoRevertsAgreementAndChargeSettlement(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	agBody, _ := json.Marshal(map[string]any{
		"amount": 30, "period": "monthly", "start_date": firstOfMonthMonthsAgo(3).Format("2006-01-02"),
		"vehicle_ids": []int64{vid},
	})
	arec := httptest.NewRecorder()
	areq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/agreements", bytes.NewReader(agBody))
	areq.SetPathValue("id", strconv.FormatInt(pid, 10))
	h.CreateAgreement(arec, areq)
	var aglist []struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(arec.Body.Bytes(), &aglist)
	var aid int64
	for _, a := range aglist {
		if a.ID > aid {
			aid = a.ID
		}
	}

	chBody, _ := json.Marshal(map[string]any{"person_id": pid, "vehicle_id": vid, "description": "Innenreinigung", "amount": 60, "quantity": 1})
	crec := httptest.NewRecorder()
	h.CreateCharge(crec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(chBody)))
	if crec.Code != http.StatusCreated {
		t.Fatalf("charge: %d %s", crec.Code, crec.Body.String())
	}

	iv := createInvoice(t, h, pid) // bills the agreement periods + the bound charge
	full := getInvoiceT(t, h, iv.ID)
	payInvoices(t, h, pid, map[string]any{"amount": full.Total, "auto": true})

	boundCharge := func() models.Charge {
		for _, c := range listChargesT(t, h, pid) {
			if c.VehicleID != nil {
				return c
			}
		}
		t.Fatalf("bound charge not found")
		return models.Charge{}
	}
	loadAg := func() models.FlatRatePeriod {
		ags, err := h.loadAgreements(t.Context(), pid, time.Now())
		if err != nil {
			t.Fatalf("load agreements: %v", err)
		}
		return agreementByID(ags, aid)
	}

	// Pre-Storno: the paid invoice settles both.
	if pre := loadAg(); len(pre.InvoicePaidPeriods) == 0 {
		t.Fatalf("pre: the paid invoice should mark the agreement's periods invoice-paid")
	}
	if c := boundCharge(); !c.Invoiced || c.InvoiceOpen {
		t.Fatalf("pre: the bound charge should read bezahlt·Rechnung, got invoiced=%v open=%v", c.Invoiced, c.InvoiceOpen)
	}

	// Storno the invoice.
	sreq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/cancel", nil)
	sreq.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	srec := httptest.NewRecorder()
	h.CancelInvoice(srec, sreq)
	if srec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", srec.Code, srec.Body.String())
	}

	// Post-Storno: both revert to "offen".
	if post := loadAg(); len(post.InvoicePaidPeriods) != 0 {
		t.Errorf("after Storno the agreement must revert to open, got invoice_paid_periods=%v", post.InvoicePaidPeriods)
	}
	if c := boundCharge(); c.Invoiced {
		t.Errorf("after Storno the bound charge must revert to 'offen', got Invoiced=%v InvoiceOpen=%v", c.Invoiced, c.InvoiceOpen)
	}
}
