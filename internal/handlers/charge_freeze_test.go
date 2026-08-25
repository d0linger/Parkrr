package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestUpdateChargeFrozenWhenInvoiced: a charge on an issued invoice must not have its
// money, owner or binding changed out from under the immutable document — repricing
// or reassigning it is blocked, but a cosmetic edit stays allowed (finding H-02).
func TestUpdateChargeFrozenWhenInvoiced(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(2).Format("2006-01-02"))

	// A vehicle-bound extra so createInvoice bills it (invoice_source, kind='charge').
	chBody, _ := json.Marshal(map[string]any{
		"person_id": pid, "vehicle_id": vid, "description": "Frostschutz", "amount": 40, "quantity": 1,
	})
	rec := httptest.NewRecorder()
	h.CreateCharge(rec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(chBody)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create charge: %d %s", rec.Code, rec.Body.String())
	}
	var chargeID int64
	if err := h.Pool.QueryRow(t.Context(), `SELECT id FROM charges WHERE person_id=$1`, pid).Scan(&chargeID); err != nil {
		t.Fatalf("find charge: %v", err)
	}
	_ = createInvoice(t, h, pid)

	var invoiced bool
	if err := h.Pool.QueryRow(t.Context(),
		`SELECT EXISTS(SELECT 1 FROM invoice_source WHERE kind='charge' AND ref_id=$1)`, chargeID).Scan(&invoiced); err != nil {
		t.Fatalf("check invoiced: %v", err)
	}
	if !invoiced {
		t.Fatalf("test setup: charge %d was not billed by createInvoice", chargeID)
	}

	update := func(body map[string]any) int {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPut, "/api/charges/"+strconv.FormatInt(chargeID, 10), bytes.NewReader(b))
		req.SetPathValue("id", strconv.FormatInt(chargeID, 10))
		rr := httptest.NewRecorder()
		h.UpdateCharge(rr, req)
		return rr.Code
	}

	// Repricing an invoiced charge → 409.
	if code := update(map[string]any{"person_id": pid, "vehicle_id": vid, "description": "Frostschutz", "amount": 99, "quantity": 1}); code != http.StatusConflict {
		t.Errorf("reprice invoiced charge: got %d, want 409", code)
	}
	// Moving it to another person (unbind + reassign) → 409.
	other := createIntegrationPerson(t, h)
	if code := update(map[string]any{"person_id": other, "description": "Frostschutz", "amount": 40, "quantity": 1}); code != http.StatusConflict {
		t.Errorf("reassign invoiced charge: got %d, want 409", code)
	}
	// Cosmetic edit (same money/owner/binding, new description) → still allowed.
	if code := update(map[string]any{"person_id": pid, "vehicle_id": vid, "description": "Frostschutz neu", "amount": 40, "quantity": 1}); code != http.StatusOK {
		t.Errorf("cosmetic edit of invoiced charge: got %d, want 200", code)
	}
	// Cent-boundary (NUMERIC(12,2) compare, not float tolerance): a value that rounds
	// to the SAME stored cent is not a billing change; one that rounds to a DIFFERENT
	// cent is.
	if code := update(map[string]any{"person_id": pid, "vehicle_id": vid, "description": "Frostschutz", "amount": 40.004, "quantity": 1}); code != http.StatusOK {
		t.Errorf("amount rounding to the same cent should be allowed: got %d, want 200", code)
	}
	if code := update(map[string]any{"person_id": pid, "vehicle_id": vid, "description": "Frostschutz", "amount": 40.006, "quantity": 1}); code != http.StatusConflict {
		t.Errorf("amount rounding to a different cent should be blocked: got %d, want 409", code)
	}
}
