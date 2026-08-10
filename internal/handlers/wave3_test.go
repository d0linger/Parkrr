package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// TestPartialCappedAtPeriodCost (W3-1): a fixed partial that exceeds the period's
// cost is rejected (would otherwise become a silent overpayment); one within cost is
// accepted.
func TestPartialCappedAtPeriodCost(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 30, "monthly", firstOfMonthMonthsAgo(2).Format("2006-01-02"))
	key := firstOfMonthMonthsAgo(2).Format("2006-01") // a completed month → cost 30

	post := func(amount float64) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"period_key": key, "paid": true, "amount": amount})
		req := httptest.NewRequest(http.MethodPost, "/api/agreements/"+strconv.FormatInt(aid, 10)+"/period-paid", bytes.NewReader(body))
		req.SetPathValue("id", strconv.FormatInt(aid, 10))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.SetAgreementPeriodPaid(rec, req)
		return rec
	}

	if rec := post(1000); rec.Code != http.StatusBadRequest {
		t.Fatalf("partial over the period cost must be 400, got %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(20); rec.Code != http.StatusOK {
		t.Errorf("partial within the period cost must be 200, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestUserKeyOf: the per-username throttle key is the "username|ip" key minus the
// exact "|ip" suffix — IP-independent and correct even for a username with a pipe.
func TestUserKeyOf(t *testing.T) {
	if got := userKeyOf("alice|1.2.3.4", "1.2.3.4"); got != "alice" {
		t.Errorf("userKeyOf = %q, want alice", got)
	}
	if got := userKeyOf("a|b|1.2.3.4", "1.2.3.4"); got != "a|b" {
		t.Errorf("userKeyOf with a piped username = %q, want a|b", got)
	}
}

// TestUsernameThrottleAcrossIPs (W3-2): failures for one username from DIFFERENT IPs
// stay under the per-(user+ip) and per-ip thresholds, but must trip the username-only
// limiter — closing the IP-rotation bypass. A different username is unaffected.
func TestUsernameThrottleAcrossIPs(t *testing.T) {
	ah := &AuthHandler{
		Handler:     &Handler{},
		Auth:        &auth.Manager{}, // trustProxy=false → ClientIP uses RemoteAddr
		Limiter:     auth.NewLoginLimiter(5, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(100, time.Minute, time.Minute),
		UserLimiter: auth.NewLoginLimiter(3, time.Minute, time.Minute),
	}
	reqFrom := func(ip string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = ip + ":1234"
		return r
	}

	// Three failures for "victim", each from a fresh IP.
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		key, ipp, ok := ah.checkRateLimit(httptest.NewRecorder(), reqFrom(ip), "victim")
		if !ok {
			t.Fatalf("attempt from %s should be allowed", ip)
		}
		ah.recordLoginFailure(key, ipp)
	}

	// A fourth try from yet another fresh IP is blocked by the username limiter.
	rec := httptest.NewRecorder()
	if _, _, ok := ah.checkRateLimit(rec, reqFrom("4.4.4.4"), "victim"); ok {
		t.Fatal("username throttle: victim should be blocked across IPs after 3 failures")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}

	// A different username from the same fresh IP is unaffected.
	if _, _, ok := ah.checkRateLimit(httptest.NewRecorder(), reqFrom("4.4.4.4"), "bystander"); !ok {
		t.Error("a different username must not inherit the victim's throttle")
	}
}
