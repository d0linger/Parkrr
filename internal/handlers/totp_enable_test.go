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
