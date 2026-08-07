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

func setAgreementMasterPaid(t *testing.T, h *Handler, aid int64, paid bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"paid": paid})
	req := httptest.NewRequest(http.MethodPost, "/api/agreements/"+strconv.FormatInt(aid, 10)+"/paid", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(aid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetAgreementPaid(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("agreement paid(%v): %d %s", paid, rec.Code, rec.Body.String())
	}
}

// TestAgreementSliderBooksPaymentAndSettlesExtras: marking a whole Pauschale paid via
// the master slider must (a) book a real Zahlungseingang for the completed rent
// periods and (b) settle the bound Zusatzkosten on its covered vehicles — so the
// balance nets to 0 and nothing is left silently owed. "offen" reverses both.
func TestAgreementSliderBooksPaymentAndSettlesExtras(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	// Cover the vehicle with a €30/month Pauschale (3 completed months + a running one).
	agBody, _ := json.Marshal(map[string]any{
		"amount": 30, "period": "monthly", "start_date": firstOfMonthMonthsAgo(3).Format("2006-01-02"),
		"vehicle_ids": []int64{vid},
	})
	arec := httptest.NewRecorder()
	areq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/agreements", bytes.NewReader(agBody))
	areq.SetPathValue("id", strconv.FormatInt(pid, 10))
	h.CreateAgreement(arec, areq)
	if arec.Code != http.StatusOK && arec.Code != http.StatusCreated {
		t.Fatalf("agreement: %d %s", arec.Code, arec.Body.String())
	}
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

	// A €20 bound extra on the covered vehicle.
	chBody, _ := json.Marshal(map[string]any{"person_id": pid, "vehicle_id": vid, "description": "Frostschutz", "amount": 20, "quantity": 1})
	chrec := httptest.NewRecorder()
	h.CreateCharge(chrec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(chBody)))
	if chrec.Code != http.StatusCreated {
		t.Fatalf("charge: %d %s", chrec.Code, chrec.Body.String())
	}

	before := personStatsT(t, h, pid)

	// Mark the whole Pauschale paid.
	setAgreementMasterPaid(t, h, aid, true)
	after := personStatsT(t, h, pid)

	if math.Abs(after.Balance) > 0.05 {
		t.Errorf("balance must net to ~0 after paying the Pauschale (rent + extra), got %.2f (before %.2f)", after.Balance, before.Balance)
	}
	// Real money-in for 3 completed months (90) + the extra (20) = 110; the running
	// month stays off-book (not yet final).
	if math.Abs(after.PaymentsTotal-110) > 0.05 {
		t.Errorf("PaymentsTotal must be 110 (90 rent + 20 extra), got %.2f", after.PaymentsTotal)
	}
	var chPaid bool
	_ = h.Pool.QueryRow(t.Context(), `SELECT paid FROM charges WHERE person_id=$1 AND vehicle_id=$2`, pid, vid).Scan(&chPaid)
	if !chPaid {
		t.Errorf("the bound extra must be marked paid by the Pauschale slider")
	}
	var extrasN int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='agreement' AND settles_ref=$1 AND settles_period='extras'`, aid).Scan(&extrasN)
	if extrasN != 1 {
		t.Errorf("expected exactly one extras payment, got %d", extrasN)
	}

	// "offen" reverses everything.
	setAgreementMasterPaid(t, h, aid, false)
	back := personStatsT(t, h, pid)
	if math.Abs(back.Balance-before.Balance) > 0.05 {
		t.Errorf("un-toggle must restore the balance: before=%.2f back=%.2f", before.Balance, back.Balance)
	}
	_ = h.Pool.QueryRow(t.Context(), `SELECT paid FROM charges WHERE person_id=$1 AND vehicle_id=$2`, pid, vid).Scan(&chPaid)
	if chPaid {
		t.Errorf("un-toggle must reset the bound extra to unpaid")
	}
	var anyPay int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='agreement' AND settles_ref=$1`, aid).Scan(&anyPay)
	if anyPay != 0 {
		t.Errorf("un-toggle must remove all of the agreement's settlement payments, got %d", anyPay)
	}
}
