package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// TestPasskeyLoginThrottle verifies the usernameless passkey login throttle
// locks a client IP after the configured number of failed attempts and reports
// a 429 with a Retry-After header, mirroring the password-login lockout.
func TestPasskeyLoginThrottle(t *testing.T) {
	ah := &AuthHandler{
		Handler: &Handler{},
		Auth:    &auth.Manager{}, // trustProxy=false -> ClientIP uses RemoteAddr
		Limiter: auth.NewLoginLimiter(3, time.Minute, time.Minute),
	}
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/finish", nil)
		r.RemoteAddr = "203.0.113.7:52000"
		return r
	}

	// First attempt is allowed and yields the IP-scoped key.
	key, ok := ah.throttlePasskeyLogin(httptest.NewRecorder(), req())
	if !ok {
		t.Fatal("first attempt should be allowed")
	}
	if key != "passkey|203.0.113.7" {
		t.Fatalf("unexpected throttle key %q", key)
	}

	// Trip the lock with the configured number of failures.
	for i := 0; i < 3; i++ {
		ah.Limiter.RecordFailure(key)
	}

	rec := httptest.NewRecorder()
	_, ok = ah.throttlePasskeyLogin(rec, req())
	if ok {
		t.Fatal("attempt after lock should be blocked")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked attempt: got status %d want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("blocked attempt should set Retry-After")
	}

	// A different IP is unaffected by the first IP's lock.
	other := httptest.NewRequest(http.MethodPost, "/", nil)
	other.RemoteAddr = "198.51.100.9:40000"
	if _, ok := ah.throttlePasskeyLogin(httptest.NewRecorder(), other); !ok {
		t.Error("a different client IP must not be throttled")
	}
}
