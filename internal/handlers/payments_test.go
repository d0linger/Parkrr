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
