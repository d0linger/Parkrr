package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// Finding SH-02: adding a second factor requires step-up — a recent primary-factor
// login OR the account password once that window has closed. This exercises the
// requireStepUp gate directly against a real session row.
//
// Runs only when PARKRR_TEST_DATABASE_URL is set (testHandler skips otherwise).
func TestRequireStepUp(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	mgr, err := auth.NewManager(h.Pool, auth.SessionConfig{MaxAge: 3600}, false, false, "a-sufficiently-long-test-secret")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ah := &AuthHandler{
		Handler:     h,
		Auth:        mgr,
		Limiter:     auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		UserLimiter: auth.NewLoginLimiter(1000, time.Minute, time.Minute),
	}

	const uname, pw = "stepup-Integration", "correct-horse-battery"
	hash, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var uid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1,$2) RETURNING id`, uname, hash).Scan(&uid); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, uname) })

	// Create a real session (created_at = now) and capture its cookie.
	rec := httptest.NewRecorder()
	if err := mgr.CreateSession(ctx, rec, httptest.NewRequest(http.MethodPost, "/login", nil), uid); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie created")
	}
	reqWithSession := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/2fa/enable", nil)
		r.AddCookie(sessionCookie)
		return r
	}

	// 1) Recent login: passes without a password.
	if w := httptest.NewRecorder(); !ah.requireStepUp(w, reqWithSession(), uname, "") {
		t.Errorf("recent session should pass step-up without a password (got %d)", w.Code)
	}

	// Backdate the session so the recent-auth window has closed.
	if _, err := h.Pool.Exec(ctx,
		`UPDATE sessions SET created_at = now() - interval '1 hour' WHERE user_id = $1`, uid); err != nil {
		t.Fatalf("backdate session: %v", err)
	}

	// 2) Window closed, no password: 403 reauth_required.
	w2 := httptest.NewRecorder()
	if ah.requireStepUp(w2, reqWithSession(), uname, "") {
		t.Error("stale session with no password must NOT pass step-up")
	}
	if w2.Code != http.StatusForbidden || !strings.Contains(w2.Body.String(), "reauth_required") {
		t.Errorf("want 403 reauth_required, got %d: %s", w2.Code, w2.Body.String())
	}

	// 3) Window closed, correct password: passes.
	if w := httptest.NewRecorder(); !ah.requireStepUp(w, reqWithSession(), uname, pw) {
		t.Errorf("correct password should pass step-up (got %d)", w.Code)
	}

	// 4) Window closed, wrong password: 403.
	w4 := httptest.NewRecorder()
	if ah.requireStepUp(w4, reqWithSession(), uname, "wrong-password") {
		t.Error("wrong password must NOT pass step-up")
	}
	if w4.Code != http.StatusForbidden {
		t.Errorf("want 403 for wrong password, got %d", w4.Code)
	}
}
