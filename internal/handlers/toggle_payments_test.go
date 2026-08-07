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

func statsBalanceT(t *testing.T, h *Handler, pid int64) float64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/persons/"+strconv.FormatInt(pid, 10)+"/stats", nil)
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	rec := httptest.NewRecorder()
	h.PersonStats(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d", rec.Code)
	}
	var s personStatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	return s.Balance
}

func setChargePaidT(t *testing.T, h *Handler, cid int64, paid bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"paid": paid})
	req := httptest.NewRequest(http.MethodPost, "/api/charges/"+strconv.FormatInt(cid, 10)+"/paid", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(cid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetChargePaid(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set charge paid=%v: %d %s", paid, rec.Code, rec.Body.String())
	}
}

// TestTogglePaymentSync (P2.3): flipping a standalone charge's paid slider records
// / removes a linked auto-payment, so the money moves consistently; idempotent.
func TestTogglePaymentSync(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "Toggle", 100, "2026-05-01")

	if b := statsBalanceT(t, h, pid); math.Abs(b-100) > 0.05 {
		t.Fatalf("initial balance should be 100, got %.2f", b)
	}
	// Toggle paid -> auto-payment -> balance 0.
	setChargePaidT(t, h, cid, true)
	if b := statsBalanceT(t, h, pid); math.Abs(b) > 0.05 {
		t.Errorf("after toggling paid, balance should be ~0, got %.2f", b)
	}
	// Idempotent: toggling paid again must not double the money.
	setChargePaidT(t, h, cid, true)
	if b := statsBalanceT(t, h, pid); math.Abs(b) > 0.05 {
		t.Errorf("re-toggle paid must not double-pay, balance %.2f", b)
	}
	// Un-toggle -> auto-payment removed -> balance back to 100.
	setChargePaidT(t, h, cid, false)
	if b := statsBalanceT(t, h, pid); math.Abs(b-100) > 0.05 {
		t.Errorf("after un-toggling, balance should be 100 again, got %.2f", b)
	}
}
