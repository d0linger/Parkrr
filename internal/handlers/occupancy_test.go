package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Exercises the occupancy SQL (joins + FILTER) against the real schema and checks
// the response invariants. Runs only when PARKRR_TEST_DATABASE_URL is set.
func TestOccupancy(t *testing.T) {
	h := testHandler(t)

	w := httptest.NewRecorder()
	h.Occupancy(w, httptest.NewRequest(http.MethodGet, "/api/occupancy", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Active int `json:"active"`
		Placed int `json:"placed"`
		Halls  []struct {
			HallID     int64  `json:"hall_id"`
			Name       string `json:"name"`
			GarageName string `json:"garage_name"`
			Placed     int    `json:"placed"`
		} `json:"halls"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Halls == nil {
		t.Error("halls must be an array, not null")
	}
	if resp.Placed > resp.Active {
		t.Errorf("invariant broken: placed (%d) > active (%d)", resp.Placed, resp.Active)
	}
	sum := 0
	for _, hh := range resp.Halls {
		if hh.Placed < 0 {
			t.Errorf("hall %d: negative placed", hh.HallID)
		}
		sum += hh.Placed
	}
	if sum > resp.Active {
		t.Errorf("sum of per-hall placed (%d) exceeds active (%d)", sum, resp.Active)
	}
}
