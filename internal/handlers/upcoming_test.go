package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// A vehicle ending in 10 days appears within a 30-day window but not a 5-day one.
// Runs only when PARKRR_TEST_DATABASE_URL is set.
func TestEndingSoon(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)

	catName := "End-Cat-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cbody, _ := json.Marshal(map[string]any{"name": catName, "default_monthly_cost": 20, "default_yearly_cost": 200})
	crec := httptest.NewRecorder()
	h.CreateCategory(crec, httptest.NewRequest(http.MethodPost, "/api/categories", bytes.NewReader(cbody)))
	var cat struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(crec.Body.Bytes(), &cat)

	vbody, _ := json.Marshal(map[string]any{
		"person_id": pid, "category_id": cat.ID, "billing_period": "monthly",
		"status": "stored", "label": "Ending-Test", "start_date": "2024-01-01",
	})
	vrec := httptest.NewRecorder()
	h.CreateVehicle(vrec, httptest.NewRequest(http.MethodPost, "/api/vehicles", bytes.NewReader(vbody)))
	if vrec.Code != http.StatusCreated {
		t.Fatalf("create vehicle: %d %s", vrec.Code, vrec.Body.String())
	}
	var veh struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(vrec.Body.Bytes(), &veh)

	if _, err := h.Pool.Exec(context.Background(),
		`UPDATE vehicles SET end_date = CURRENT_DATE + 10 WHERE id = $1`, veh.ID); err != nil {
		t.Fatalf("set end_date: %v", err)
	}

	call := func(days string) []map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/vehicles/ending-soon?days="+days, nil)
		w := httptest.NewRecorder()
		h.EndingSoon(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ending-soon days=%s: %d %s", days, w.Code, w.Body.String())
		}
		var out []map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	has := func(list []map[string]any) bool {
		for _, e := range list {
			if int64(e["id"].(float64)) == veh.ID {
				return true
			}
		}
		return false
	}

	if !has(call("30")) {
		t.Error("vehicle ending in 10 days must appear within a 30-day window")
	}
	if has(call("5")) {
		t.Error("vehicle ending in 10 days must NOT appear within a 5-day window")
	}

	// Boundary: a contract ending exactly on CURRENT_DATE + days must be included
	// (inclusive upper bound).
	if _, err := h.Pool.Exec(context.Background(),
		`UPDATE vehicles SET end_date = CURRENT_DATE + 30 WHERE id = $1`, veh.ID); err != nil {
		t.Fatalf("set end_date +30: %v", err)
	}
	if !has(call("30")) {
		t.Error("vehicle ending exactly on day 30 must appear within a 30-day window")
	}
}
