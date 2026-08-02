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
