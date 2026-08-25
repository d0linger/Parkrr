package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestApplyCreditDoesNotOverAllocateSmallPayment pins the H-01 fix: ApplyCredit must
// book each drawdown against a payment that actually has enough unspent value, not
// blindly against the newest payment. Otherwise a tiny latest payment carries a large
// allocation, and reversing that tiny payment would wrongly reopen the large amount.
func TestApplyCreditDoesNotOverAllocateSmallPayment(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)

	// Pure Guthaben: a big payment first, then a small "latest" one — neither allocated
	// (there are no open items yet).
	if rec := postPayment(t, h, pid, map[string]any{"amount": 100, "method": "bar", "allocate": false}); rec.Code != http.StatusCreated {
		t.Fatalf("payment A: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postPayment(t, h, pid, map[string]any{"amount": 1, "method": "bar", "allocate": false}); rec.Code != http.StatusCreated {
		t.Fatalf("payment B: %d %s", rec.Code, rec.Body.String())
	}
	var bigPay, smallPay int64
	if err := h.Pool.QueryRow(t.Context(), `SELECT id FROM payments WHERE person_id=$1 AND amount=100`, pid).Scan(&bigPay); err != nil {
		t.Fatalf("find big payment: %v", err)
	}
	if err := h.Pool.QueryRow(t.Context(), `SELECT id FROM payments WHERE person_id=$1 AND amount=1`, pid).Scan(&smallPay); err != nil {
		t.Fatalf("find small payment: %v", err)
	}

	// An open item only the big payment can cover.
	c := mkChargeP(t, h, pid, "big open", 100, "2026-03-01")

	areq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/apply-credit", nil)
	areq.SetPathValue("id", strconv.FormatInt(pid, 10))
	arec := httptest.NewRecorder()
	h.ApplyCredit(arec, areq)
	if arec.Code != http.StatusOK {
		t.Fatalf("apply-credit: %d %s", arec.Code, arec.Body.String())
	}
	if !chargePaid(t, h, c) {
		t.Fatal("the open charge should be settled from Guthaben")
	}

	// The €100 drawdown must be booked against the €100 payment, NOT the €1 latest one.
	var allocPay int64
	if err := h.Pool.QueryRow(t.Context(),
		`SELECT payment_id FROM payment_allocations WHERE kind='charge' AND ref_id=$1`, c).Scan(&allocPay); err != nil {
		t.Fatalf("find allocation: %v", err)
	}
	if allocPay != bigPay {
		t.Errorf("charge allocated to payment %d, want the 100 EUR payment %d (not the 1 EUR payment %d)", allocPay, bigPay, smallPay)
	}

	// Conservation: no payment allocates more than its own amount.
	var overallocated int
	if err := h.Pool.QueryRow(t.Context(), `
		SELECT count(*) FROM payments p
		 WHERE COALESCE((SELECT SUM(amount) FROM payment_allocations WHERE payment_id=p.id),0)
		     + COALESCE((SELECT SUM(amount) FROM invoice_payments   WHERE payment_id=p.id),0)
		     > p.amount + 0.005`).Scan(&overallocated); err != nil {
		t.Fatalf("conservation query: %v", err)
	}
	if overallocated != 0 {
		t.Errorf("%d payment(s) allocate more than their amount (H-01 over-allocation)", overallocated)
	}
}

// TestApplyCreditSkipsUnaffordableButSettlesLaterSmaller pins the break->continue
// fix: with €60 credit and an ordered €100 then €50 open item, the €100 exceeds the
// budget but the €50 must still be settled — the loop skips the unaffordable item
// instead of stopping at it.
func TestApplyCreditSkipsUnaffordableButSettlesLaterSmaller(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	if rec := postPayment(t, h, pid, map[string]any{"amount": 60, "method": "bar", "allocate": false}); rec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", rec.Code, rec.Body.String())
	}
	cBig := mkChargeP(t, h, pid, "big", 100, "2026-01-10")
	cSmall := mkChargeP(t, h, pid, "small", 50, "2026-02-10")

	areq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/apply-credit", nil)
	areq.SetPathValue("id", strconv.FormatInt(pid, 10))
	arec := httptest.NewRecorder()
	h.ApplyCredit(arec, areq)
	if arec.Code != http.StatusOK {
		t.Fatalf("apply-credit: %d %s", arec.Code, arec.Body.String())
	}
	if chargePaid(t, h, cBig) {
		t.Error("the 100 EUR item should NOT be settled (exceeds the 60 EUR budget)")
	}
	if !chargePaid(t, h, cSmall) {
		t.Error("the 50 EUR item should be settled from the 60 EUR credit (not skipped by a break)")
	}
}
