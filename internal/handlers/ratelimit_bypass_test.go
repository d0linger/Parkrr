package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// Usernames are matched case-insensitively in the database, so the login
// throttle must treat "admin" and "Admin" as the same key — otherwise varying
// the casing hands the attacker a fresh rate-limit bucket per variant.
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

	key1, ok := ah.checkRateLimit(httptest.NewRecorder(), req(), "admin")
	if !ok {
		t.Fatal("first attempt should be allowed")
	}

	// Lock "admin" for this IP.
	ah.Limiter.RecordFailure(key1)
	ah.Limiter.RecordFailure(key1)

	rec1 := httptest.NewRecorder()
	if _, ok = ah.checkRateLimit(rec1, req(), "admin"); ok {
		t.Fatal("admin should be blocked after 2 failures")
	}
	if rec1.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for admin, got %d", rec1.Code)
	}

	// The same account under different casing must share the lockout.
	if _, ok = ah.checkRateLimit(httptest.NewRecorder(), req(), "Admin"); ok {
		t.Error("casing bypass: 'Admin' was allowed while 'admin' was locked")
	}
}
