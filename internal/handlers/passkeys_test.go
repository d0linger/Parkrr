package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestPasskeyRegisterBegin_NameLength(t *testing.T) {
	wa, err := auth.NewWebAuthnService(nil, "example.com", "Example", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("failed to create webauthn service: %v", err)
	}

	ah := &AuthHandler{
		Handler:  &Handler{},
		WebAuthn: wa,
	}

	// Create request with extremely long name
	body := map[string]string{
		"name": strings.Repeat("a", maxNameLen+1),
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/passkeys/register/begin", bytes.NewReader(b))
	w := httptest.NewRecorder()

	ah.PasskeyRegisterBegin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp["error"] != "name is too long" {
		t.Errorf("got error %q, want %q", resp["error"], "name is too long")
	}
}

func TestPasskeyRegisterBegin_PasswordLength(t *testing.T) {
	wa, err := auth.NewWebAuthnService(nil, "example.com", "Example", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("failed to create webauthn service: %v", err)
	}

	ah := &AuthHandler{
		Handler:  &Handler{},
		WebAuthn: wa,
	}

	// Create request with password > maxPasswordLen (72 bytes)
	body := map[string]string{
		"password": strings.Repeat("a", maxPasswordLen+1),
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/passkeys/register/begin", bytes.NewReader(b))
	w := httptest.NewRecorder()

	ah.PasskeyRegisterBegin(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestPasskeyRegisterFinish_PerAccountRateLimit(t *testing.T) {
	ah := &AuthHandler{
		Handler:     &Handler{},
		Auth:        &auth.Manager{},
		Limiter:     auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		UserLimiter: auth.NewStickyLoginLimiter(3, time.Minute, time.Minute),
	}

	reqFrom := func(ip string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/passkeys/register/finish", nil)
		r.RemoteAddr = ip + ":1234"
		return r
	}

	// Record 3 registration failures for user "victim" across 3 different IPs.
	for i, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		key, cip, ok := ah.checkRateLimit(httptest.NewRecorder(), reqFrom(ip), "victim")
		if !ok {
			t.Fatalf("attempt %d from %s should be allowed", i, ip)
		}
		ah.recordReauthFailure(key, cip)
	}

	// The 4th attempt from a fresh IP must be rate-limited per-account.
	rec := httptest.NewRecorder()
	if _, _, ok := ah.checkRateLimit(rec, reqFrom("4.4.4.4"), "victim"); ok {
		t.Fatal("per-account lockout should trip after registration failures across rotating IPs")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 from per-account throttle, got %d", rec.Code)
	}
}
