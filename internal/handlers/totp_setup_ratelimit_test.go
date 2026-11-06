package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

func TestTOTPSetup_CeremonyRateLimit(t *testing.T) {
	const maxSetupStarts = 3
	ah := &AuthHandler{
		Handler:         &Handler{},
		Auth:            &auth.Manager{},
		CeremonyLimiter: auth.NewStickyLoginLimiter(maxSetupStarts, time.Minute, time.Minute),
	}

	u := &models.User{ID: 1, Username: "totp-setup-user", TOTPEnabled: false}
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/setup", nil)
		return r.WithContext(auth.ContextWithUser(context.Background(), u))
	}

	// Consume maxSetupStarts tokens. Note: DB call in TOTPSetup will fail if allowed,
	// but we test that the rate limiter permits maxSetupStarts and blocks subsequent ones.
	for i := 0; i < maxSetupStarts; i++ {
		w := httptest.NewRecorder()
		ah.TOTPSetup(w, req())
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("setup attempt %d should not be rate limited", i)
		}
	}

	// The (maxSetupStarts + 1)-th call must be blocked by CeremonyLimiter with 429.
	w := httptest.NewRecorder()
	ah.TOTPSetup(w, req())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 Too Many Requests when CeremonyLimiter exhausted, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 response")
	}
}

// TestTOTPSetup_ExistingLockout verifies that an active authentication lockout
// (IP-level Limiter or account-level UserLimiter) blocks starting TOTP setup with 429.
func TestTOTPSetup_ExistingLockout(t *testing.T) {
	ah := &AuthHandler{
		Handler:         &Handler{},
		Auth:            &auth.Manager{},
		Limiter:         auth.NewLoginLimiter(3, time.Minute, time.Minute),
		UserLimiter:     auth.NewStickyLoginLimiter(3, time.Minute, time.Minute),
		CeremonyLimiter: auth.NewStickyLoginLimiter(100, time.Minute, time.Minute),
	}

	const uname = "totp-lockout-user"
	u := &models.User{ID: 2, Username: uname, TOTPEnabled: false}
	reqFrom := func(ip string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/setup", nil)
		r.RemoteAddr = ip + ":1234"
		return r.WithContext(auth.ContextWithUser(context.Background(), u))
	}

	// Lock the IP limiter with 3 failed attempts.
	for i := 0; i < 3; i++ {
		key, cip, ok := ah.checkRateLimit(httptest.NewRecorder(), reqFrom("192.0.2.1"), uname)
		if !ok {
			t.Fatalf("attempt %d should be allowed during test setup", i)
		}
		ah.recordReauthFailure(key, cip)
	}

	// IP is locked out: TOTPSetup from that IP must be blocked with 429.
	w := httptest.NewRecorder()
	ah.TOTPSetup(w, reqFrom("192.0.2.1"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 from IP lockout, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on throttled response")
	}

	// Reset IP limiter for UserLimiter test.
	ah.Limiter.Reset(uname + "|192.0.2.1")
	ah.UserLimiter.Reset(uname)

	// Lock the account across different IPs using UserLimiter.
	for i, ip := range []string{"192.0.2.10", "192.0.2.11", "192.0.2.12"} {
		key, cip, ok := ah.checkRateLimit(httptest.NewRecorder(), reqFrom(ip), uname)
		if !ok {
			t.Fatalf("attempt %d from %s should be allowed during test setup", i, ip)
		}
		ah.recordReauthFailure(key, cip)
	}

	// Per-account throttle is active: request from a fresh IP must be rejected with 429.
	w = httptest.NewRecorder()
	ah.TOTPSetup(w, reqFrom("192.0.2.99"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 from account lockout, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on throttled response")
	}
}
