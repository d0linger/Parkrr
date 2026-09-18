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
