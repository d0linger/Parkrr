package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression for the bcrypt 72-byte limit: over-long passwords must be
// rejected with 400 by validation, not bubble up as a 500 from HashPassword
// (x/crypto's bcrypt refuses inputs longer than 72 bytes).
func TestPasswordLengthPolicy(t *testing.T) {
	long := strings.Repeat("a", maxPasswordLen+1)

	if validPasswordLength(long) {
		t.Error("password above the maximum must be rejected")
	}
	if validPasswordLength(strings.Repeat("a", minPasswordLen-1)) {
		t.Error("password below the minimum must be rejected")
	}
	if !validPasswordLength(strings.Repeat("a", minPasswordLen)) ||
		!validPasswordLength(strings.Repeat("a", maxPasswordLen)) {
		t.Error("boundary-length passwords must be accepted")
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
}
