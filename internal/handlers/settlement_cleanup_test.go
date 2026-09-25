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

func setRecurringMasterPaid(t *testing.T, h *Handler, id int64, paid bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"paid": paid})
	req := httptest.NewRequest(http.MethodPost, "/api/recurring/"+strconv.FormatInt(id, 10)+"/paid", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetRecurringChargePaid(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("recurring paid(%v): %d %s", paid, rec.Code, rec.Body.String())
	}
}

func mkRecurring(t *testing.T, h *Handler, pid int64, amount float64, start string) int64 {
	t.Helper()
	a := amount
	body, _ := json.Marshal(recurringRequest{Description: "Versicherung", Amount: &a, Period: "monthly", StartDate: start})
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/recurring", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateRecurringCharge(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("recurring: %d %s", rec.Code, rec.Body.String())
	}
	var one struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &one)
	if one.ID != 0 {
		return one.ID
	}
	var list []struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	var id int64
	for _, x := range list {
		if x.ID > id {
			id = x.ID
		}
	}
	return id
}

// TestRecurringMasterSliderBooksPayment: the Nebenkosten master slider now books a
// real Zahlungseingang per completed period and reverses cleanly on "offen".
func TestRecurringMasterSliderBooksPayment(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	rid := mkRecurring(t, h, pid, 20, firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	before := personStatsT(t, h, pid)
	setRecurringMasterPaid(t, h, rid, true)
	after := personStatsT(t, h, pid)

	// 3 completed months × 20 = 60 booked. The running month is NOT settled by the
	// master slider (BIL-01): it stays owed until it closes and is invoiced then.
	if math.Abs(after.PaymentsTotal-60) > 0.05 {
		t.Errorf("recurring master paid must book 60 (3×20), got %.2f", after.PaymentsTotal)
	}
	if math.Abs(after.Balance-(before.Balance-60)) > 0.05 || after.Balance < -0.05 {
		t.Errorf("balance must drop by exactly the 60 booked (running month still owed): before=%.2f after=%.2f",
			before.Balance, after.Balance)
	}

	setRecurringMasterPaid(t, h, rid, false)
	back := personStatsT(t, h, pid)
	if math.Abs(back.Balance-before.Balance) > 0.05 {
		t.Errorf("un-toggle must restore balance: before=%.2f back=%.2f", before.Balance, back.Balance)
	}
	var n int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='recurring' AND settles_ref=$1`, rid).Scan(&n)
	if n != 0 {
		t.Errorf("un-toggle must remove the recurring settle payments, got %d", n)
	}
}

// TestDeleteSettledAgreementIsBlocked: a Pauschale with settlement history is an
// accounting principal. It and its payment evidence must survive deletion attempts.
func TestDeleteSettledAgreementIsBlocked(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))
	agBody, _ := json.Marshal(map[string]any{
		"amount": 30, "period": "monthly", "start_date": firstOfMonthMonthsAgo(3).Format("2006-01-02"),
		"end_date":    firstOfMonthMonthsAgo(0).AddDate(0, 0, -1).Format("2006-01-02"),
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
	setAgreementMasterPaid(t, h, aid, true) // books settle payments

	var pre int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='agreement' AND settles_ref=$1`, aid).Scan(&pre)
	if pre == 0 {
		t.Fatalf("precondition: paid agreement should have settle payments")
	}

	dreq := httptest.NewRequest(http.MethodDelete, "/api/agreements/"+strconv.FormatInt(aid, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(aid, 10))
	drec := httptest.NewRecorder()
	h.DeleteAgreement(drec, dreq)
	if drec.Code != http.StatusConflict {
		t.Fatalf("delete settled agreement: %d %s", drec.Code, drec.Body.String())
	}

	var post int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='agreement' AND settles_ref=$1`, aid).Scan(&post)
	if post != pre {
		t.Errorf("blocked delete must preserve all %d settlement payment(s), %d remain", pre, post)
	}
}

// TestDeleteRecurringClearsSettlementPayments: same for a paid Nebenkosten.
func TestDeleteRecurringClearsSettlementPayments(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	rid := mkRecurring(t, h, pid, 20, firstOfMonthMonthsAgo(3).Format("2006-01-02"))
	setRecurringMasterPaid(t, h, rid, true)

	var pre int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='recurring' AND settles_ref=$1`, rid).Scan(&pre)
	if pre == 0 {
		t.Fatalf("precondition: paid recurring should have settle payments")
	}

	dreq := httptest.NewRequest(http.MethodDelete, "/api/recurring/"+strconv.FormatInt(rid, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(rid, 10))
	drec := httptest.NewRecorder()
	h.DeleteRecurringCharge(drec, dreq)
	if drec.Code != http.StatusOK {
		t.Fatalf("delete recurring: %d %s", drec.Code, drec.Body.String())
	}

	var post int
	_ = h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM payments WHERE settles_kind='recurring' AND settles_ref=$1`, rid).Scan(&post)
	if post != 0 {
		t.Errorf("deleting the Nebenkosten must remove its settle payments, %d remain", post)
	}
}
