package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

func TestTOTPEnableValidation(t *testing.T) {
	ah := &AuthHandler{Handler: &Handler{}}

	// Reject invalid TOTP code length up front.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/enable",
		strings.NewReader(`{"code":""}`))
	ctx := auth.ContextWithUser(context.Background(), &models.User{ID: 1, Username: "testuser"})
	req = req.WithContext(ctx)

	ah.TOTPEnable(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("TOTPEnable with empty code: expected 400, got %d", rec.Code)
	}

	// Reject code exceeding maxTOTPCodeLen up front.
	longCode := strings.Repeat("1", 21)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/2fa/enable",
		strings.NewReader(`{"code":"`+longCode+`"}`))
	req = req.WithContext(ctx)

	ah.TOTPEnable(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("TOTPEnable with over-long code: expected 400, got %d", rec.Code)
	}
}

func TestTOTPDisableResetsFailuresAndStep(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	pw := "securepassword123"
	hash, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}

	var uid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, totp_enabled, totp_failures, totp_locked_until, last_totp_step)
		 VALUES ('totp-disable-test', $1, true, 5, now() + interval '1 hour', 12345)
		 RETURNING id`, hash).Scan(&uid); err != nil {
		t.Fatalf("INSERT user error: %v", err)
	}

	mgr, err := auth.NewManager(h.Pool, auth.SessionConfig{MaxAge: 3600}, false, false, "a-sufficiently-long-test-secret")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ah := &AuthHandler{
		Handler:     h,
		Auth:        mgr,
		Limiter:     auth.NewLoginLimiter(5, 10, 10),
		UserLimiter: auth.NewLoginLimiter(5, 10, 10),
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/disable",
		strings.NewReader(`{"password":"`+pw+`"}`))
	req.Header.Set("Content-Type", "application/json")
	u := &models.User{ID: uid, Username: "totp-disable-test", TOTPEnabled: true}
	req = req.WithContext(auth.ContextWithUser(ctx, u))

	ah.TOTPDisable(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("TOTPDisable expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var enabled bool
	var failures int
	var lockedUntil *string
	var lastStep int64
	if err := h.Pool.QueryRow(ctx,
		`SELECT totp_enabled, totp_failures, totp_locked_until::text, last_totp_step FROM users WHERE id=$1`, uid).
		Scan(&enabled, &failures, &lockedUntil, &lastStep); err != nil {
		t.Fatalf("SELECT user error: %v", err)
	}

	if enabled {
		t.Errorf("expected totp_enabled to be false")
	}
	if failures != 0 {
		t.Errorf("expected totp_failures=0, got %d", failures)
	}
	if lockedUntil != nil {
		t.Errorf("expected totp_locked_until to be nil, got %v", *lockedUntil)
	}
	if lastStep != 0 {
		t.Errorf("expected last_totp_step=0, got %d", lastStep)
	}
}
