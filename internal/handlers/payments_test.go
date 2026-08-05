package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/preining/parkrr/internal/models"
)

// createIntegrationPerson inserts a person (cleaned up by cleanupPersons) and
// returns its id.
func createIntegrationPerson(t *testing.T, h *Handler) int64 {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"first_name": "Pay", "last_name": "Integration"})
	req := httptest.NewRequest(http.MethodPost, "/api/persons", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.CreatePerson(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create person: status %d body %s", rec.Code, rec.Body.String())
	}
	var p models.Person
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.ID == 0 {
		t.Fatalf("decode person: %v", err)
	}
	return p.ID
}

func postPayment(t *testing.T, h *Handler, pid int64, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/payments", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	rec := httptest.NewRecorder()
	h.CreatePayment(rec, req)
	return rec
}

// TestCreateAndListPayment exercises the full money-in path against a real DB —
// the case that was missing when the 023 migration collided with the reserved
// payments table and dropped created_by.
func TestCreateAndListPayment(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)

	rec := postPayment(t, h, pid, map[string]any{
		"amount": 49.99, "method": "ueberweisung", "paid_on": "2026-08-02", "note": "test",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create payment: status %d body %s", rec.Code, rec.Body.String())
	}
	var created payment
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode payment: %v", err)
	}
	if created.ID == 0 || created.Amount != 49.99 || created.Method != "ueberweisung" {
		t.Fatalf("unexpected created payment: %+v", created)
	}

	// List returns the payment.
	lreq := httptest.NewRequest(http.MethodGet, "/api/persons/"+strconv.FormatInt(pid, 10)+"/payments", nil)
	lreq.SetPathValue("id", strconv.FormatInt(pid, 10))
	lrec := httptest.NewRecorder()
	h.ListPayments(lrec, lreq)
	if lrec.Code != http.StatusOK {
		t.Fatalf("list: status %d", lrec.Code)
	}
	var list []payment
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("expected the created payment in the list, got %+v", list)
	}
}

// TestPaymentAllocationOldestFirst records a payment with allocate=true and
// asserts it settles the oldest open standalone charge first, stopping when the
// remaining amount no longer covers the next item.
func TestPaymentAllocationOldestFirst(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)

	mkCharge := func(desc string, amount float64, on string) int64 {
		body, _ := json.Marshal(map[string]any{
			"person_id": pid, "description": desc, "amount": amount, "quantity": 1, "charged_on": on,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.CreateCharge(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create charge: %d %s", rec.Code, rec.Body.String())
		}
		var out struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.ID
	}
	older := mkCharge("older", 50, "2026-01-10")
	newer := mkCharge("newer", 80, "2026-06-10")

	// 60 € covers the 50 € older charge; 10 € left can't cover the 80 € newer one.
	rec := postPayment(t, h, pid, map[string]any{"amount": 60, "method": "bar", "allocate": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Settled int `json:"settled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Settled != 1 {
		t.Errorf("expected 1 item settled, got %d", res.Settled)
	}

	paid := map[int64]bool{}
	rows, err := h.Pool.Query(t.Context(), `SELECT id, paid FROM charges WHERE person_id=$1`, pid)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p bool
		if err := rows.Scan(&id, &p); err != nil {
			t.Fatal(err)
		}
		paid[id] = p
	}
	if !paid[older] {
		t.Error("oldest charge should be settled")
	}
	if paid[newer] {
		t.Error("newer charge should remain open (amount exhausted)")
	}
}

func mkChargeP(t *testing.T, h *Handler, pid int64, desc string, amount float64, on string) int64 {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"person_id": pid, "description": desc, "amount": amount, "quantity": 1, "charged_on": on})
	rec := httptest.NewRecorder()
	h.CreateCharge(rec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("charge: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.ID
}

func chargePaid(t *testing.T, h *Handler, id int64) bool {
	t.Helper()
	var paid bool
	if err := h.Pool.QueryRow(t.Context(), `SELECT paid FROM charges WHERE id=$1`, id).Scan(&paid); err != nil {
		t.Fatalf("charge paid: %v", err)
	}
	return paid
}

func personCreditVal(t *testing.T, h *Handler, pid int64) float64 {
	t.Helper()
	// Guthaben is the production balance path (−Balance), the single source of truth.
	return personStatsT(t, h, pid).Credit
}

// TestPaymentExplicitSelectionAndDelete: an explicit selection stamps exactly the
// chosen item; deleting the payment reverts it (interdependence).
func TestPaymentExplicitSelectionAndDelete(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	older := mkChargeP(t, h, pid, "older", 50, "2026-01-10")
	newer := mkChargeP(t, h, pid, "newer", 80, "2026-06-10")

	rec := postPayment(t, h, pid, map[string]any{
		"amount": 200, "method": "bar",
		"allocations": []map[string]any{{"kind": "charge", "id": newer}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		ID      int64 `json:"id"`
		Settled int   `json:"settled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Settled != 1 || !chargePaid(t, h, newer) || chargePaid(t, h, older) {
		t.Fatalf("explicit selection wrong: settled=%d newerPaid=%v olderPaid=%v", res.Settled, chargePaid(t, h, newer), chargePaid(t, h, older))
	}
	// True overpayment: paid 200, owed 130 (both charges) -> Guthaben 70 (NOT 120:
	// the unstamped 50 charge is still a real cost, not credit).
	if c := personCreditVal(t, h, pid); c != 70 {
		t.Errorf("expected Guthaben 70, got %.2f", c)
	}
	// Delete the payment -> the newer charge reverts to open, credit clears.
	dreq := httptest.NewRequest(http.MethodDelete, "/api/payments/"+strconv.FormatInt(res.ID, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(res.ID, 10))
	drec := httptest.NewRecorder()
	h.DeletePayment(drec, dreq)
	if drec.Code != http.StatusOK {
		t.Fatalf("delete: %d", drec.Code)
	}
	if chargePaid(t, h, newer) {
		t.Error("newer charge should revert to open after payment delete")
	}
	if c := personCreditVal(t, h, pid); c != 0 {
		t.Errorf("expected Guthaben 0 after delete, got %.2f", c)
	}
}

// TestOverpaymentAndApplyCredit: overpaying leaves Guthaben; applying it later
// settles a newly-open item.
func TestOverpaymentAndApplyCredit(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	c1 := mkChargeP(t, h, pid, "first", 100, "2026-01-10")

	rec := postPayment(t, h, pid, map[string]any{"amount": 250, "method": "ueberweisung", "allocate": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", rec.Code, rec.Body.String())
	}
	if !chargePaid(t, h, c1) {
		t.Error("first charge should be stamped")
	}
	if c := personCreditVal(t, h, pid); c != 150 {
		t.Fatalf("expected Guthaben 150, got %.2f", c)
	}

	c2 := mkChargeP(t, h, pid, "later", 120, "2026-06-10")
	areq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/apply-credit", nil)
	areq.SetPathValue("id", strconv.FormatInt(pid, 10))
	arec := httptest.NewRecorder()
	h.ApplyCredit(arec, areq)
	if arec.Code != http.StatusOK {
		t.Fatalf("apply-credit: %d %s", arec.Code, arec.Body.String())
	}
	if !chargePaid(t, h, c2) {
		t.Error("apply-credit should stamp the newly-open charge from Guthaben")
	}
	if c := personCreditVal(t, h, pid); c != 30 {
		t.Errorf("expected Guthaben 30 after applying, got %.2f", c)
	}
}

func TestCreatePaymentValidation(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)

	if rec := postPayment(t, h, pid, map[string]any{"amount": 0, "method": "bar"}); rec.Code != http.StatusBadRequest {
		t.Errorf("zero amount should be rejected, got %d", rec.Code)
	}
	if rec := postPayment(t, h, pid, map[string]any{"amount": 10, "method": "bitcoin"}); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown method should be rejected, got %d", rec.Code)
	}
}
