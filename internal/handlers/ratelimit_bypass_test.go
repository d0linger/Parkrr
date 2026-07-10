package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

func TestRateLimitBypass_Casing(t *testing.T) {
	ah := &AuthHandler{
		Handler: &Handler{},
		Auth:    &auth.Manager{}, // trustProxy=false -> ClientIP uses RemoteAddr
		Limiter: auth.NewLoginLimiter(2, time.Minute, time.Minute),
	}

	ip := "1.2.3.4"
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = ip + ":1234"
		return r
	}

	// First attempt with "admin"
	key1, ok := ah.checkRateLimit(httptest.NewRecorder(), req(), "admin")
	if !ok {
		t.Fatal("first attempt should be allowed")
	}

	// Fail "admin" twice to lock it
	ah.Limiter.RecordFailure(key1)
	ah.Limiter.RecordFailure(key1)

	// Now "admin" should be blocked
	rec1 := httptest.NewRecorder()
	_, ok = ah.checkRateLimit(rec1, req(), "admin")
	if ok {
		t.Fatal("admin should be blocked after 2 failures")
	}
	if rec1.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for admin, got %d", rec1.Code)
	}

	// Vulnerability check: "Admin" should also be blocked because it's the same user in the DB.
	// WITHOUT THE FIX, this will likely pass (not be blocked) because the key is "Admin|1.2.3.4".
	rec2 := httptest.NewRecorder()
	_, ok = ah.checkRateLimit(rec2, req(), "Admin")

	// We WANT it to be blocked. If it's NOT blocked, it's a vulnerability.
	// For the reproduction test, we can assert that it IS currently NOT blocked (to show the bug).
	// But it's better to write a test that fails when the bug is present.
	if ok {
		t.Errorf("VULNERABILITY: 'Admin' was allowed while 'admin' was locked. Casing bypass detected.")
	}
}
