package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// Regression for the bcrypt 72-byte limit: over-long passwords must be
// rejected with 400 by validation, not bubble up as a 500 from HashPassword
// (x/crypto's bcrypt refuses inputs longer than 72 bytes).
func TestPasswordLengthPolicy(t *testing.T) {
	long := strings.Repeat("a", 73)

	// The contract is a concrete 8-72 BYTES (bcrypt's input limit), asserted
	// with literals on purpose so drifting constants would fail here.
	for _, tc := range []struct {
		pw string
		ok bool
	}{
		{strings.Repeat("a", 7), false},
		{strings.Repeat("a", 8), true},
		{strings.Repeat("a", 72), true},
		{long, false},
		// len() counts bytes, not runes: 18 four-byte emoji (72 bytes) fit
		// exactly, 19 (76 bytes) do not.
		{strings.Repeat("\U0001F600", 18), true},
		{strings.Repeat("\U0001F600", 19), false},
	} {
		if got := validPasswordLength(tc.pw); got != tc.ok {
			t.Errorf("validPasswordLength(%d bytes) = %v, want %v", len(tc.pw), got, tc.ok)
		}
	}

	// ChangePassword answers 400 before touching the rate limiter, HIBP or
	// the database, so a zero-value handler suffices.
	ah := &AuthHandler{Handler: &Handler{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password",
		strings.NewReader(`{"current_password":"x","new_password":"`+long+`"}`))
	ah.ChangePassword(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("ChangePassword with %d-byte password: expected 400, got %d", len(long), rec.Code)
	}

	// CreateUser validates length in the same guard as the username.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/users",
		strings.NewReader(`{"username":"u","password":"`+long+`"}`))
	(&Handler{}).CreateUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("CreateUser with %d-byte password: expected 400, got %d", len(long), rec.Code)
	}

	// UpdateUser validates an optional password change before the
	// last-admin check or user SELECT hit the database.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/users/1",
		strings.NewReader(`{"username":"u","role":"editor","password":"`+long+`"}`))
	req.SetPathValue("id", "1")
	(&Handler{}).UpdateUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("UpdateUser with %d-byte password: expected 400, got %d", len(long), rec.Code)
	}

	// TOTPDisable validates password length early.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/2fa/disable",
		strings.NewReader(`{"password":"`+long+`"}`))
	ah.TOTPDisable(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("TOTPDisable with %d-byte password: expected 403, got %d", len(long), rec.Code)
	}

	// TOTPRegenerateBackup validates password length early.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/2fa/backup-codes/regenerate",
		strings.NewReader(`{"password":"`+long+`"}`))
	u := &models.User{TOTPEnabled: true}
	req = req.WithContext(auth.ContextWithUser(req.Context(), u))
	ah.TOTPRegenerateBackup(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("TOTPRegenerateBackup with %d-byte password: expected 403, got %d", len(long), rec.Code)
	}
}

// Usernames are capped at 100 bytes; over-long ones are rejected before any
// state lookup (they also bound the rate-limiter key size).
func TestUsernameLengthPolicy(t *testing.T) {
	long := strings.Repeat("a", 101)

	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"", false},
		{"a", true},
		{strings.Repeat("a", 100), true},
		{long, false},
	} {
		if got := validUsernameLength(tc.name); got != tc.ok {
			t.Errorf("validUsernameLength(%d bytes) = %v, want %v", len(tc.name), got, tc.ok)
		}
	}

	// Login rejects an over-long username with a generic 401 before any lookup.
	ah := &AuthHandler{Handler: &Handler{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"`+long+`","password":"password"}`))
	ah.Login(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Login with %d-byte username: expected 401, got %d", len(long), rec.Code)
	}

	// CreateUser and UpdateUser reject it with 400.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/users",
		strings.NewReader(`{"username":"`+long+`","password":"password"}`))
	(&Handler{}).CreateUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("CreateUser with %d-byte username: expected 400, got %d", len(long), rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/users/1",
		strings.NewReader(`{"username":"`+long+`","role":"editor"}`))
	req.SetPathValue("id", "1")
	(&Handler{}).UpdateUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("UpdateUser with %d-byte username: expected 400, got %d", len(long), rec.Code)
	}

	// ChangePassword rejects an over-long current password with 403 up front
	// (bcrypt caps at 72 bytes, so it could never match) — before hitting the
	// rate limiter or an authentication attempt.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/change-password",
		strings.NewReader(`{"current_password":"`+long+`","new_password":"newpassword1"}`))
	ah.ChangePassword(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("ChangePassword with %d-byte current password: expected 403, got %d", len(long), rec.Code)
	}
}
