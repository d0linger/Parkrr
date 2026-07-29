package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestChangePasswordRejectsIdentical verifies the rotation policy: a new
// password identical to the current one is rejected up front (400), before any
// rate-limit or bcrypt work — so a forced reset can't be no-op'd by resubmitting
// the same password. (Ported from Treckrr PR #81.)
func TestChangePasswordRejectsIdentical(t *testing.T) {
	ah := &AuthHandler{Handler: &Handler{}}

	const pw = "samePassword123"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password",
		strings.NewReader(`{"current_password":"`+pw+`","new_password":"`+pw+`"}`))
	ah.ChangePassword(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("identical new password: expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "must differ") {
		t.Errorf("expected a 'must differ' message, got %q", rec.Body.String())
	}
}

// TestChangePasswordTooShortStillRejected guards ordering: length validation
// still runs (a too-short new password is a 400 for length, not rotation).
func TestChangePasswordTooShortStillRejected(t *testing.T) {
	ah := &AuthHandler{Handler: &Handler{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password",
		strings.NewReader(`{"current_password":"short","new_password":"short"}`))
	ah.ChangePassword(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("too-short password: expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "between 8 and 72") {
		t.Errorf("expected length error, got %q", rec.Body.String())
	}
}
