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

func TestTOTPEnableErrorHandling(t *testing.T) {
	ah := &AuthHandler{Handler: &Handler{}}

	// 1. Invalid JSON request body returns 400.
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/enable", strings.NewReader("invalid-json"))
		ctx := auth.ContextWithUser(context.Background(), &models.User{ID: 1, Username: "testuser"})
		req = req.WithContext(ctx)

		ah.TOTPEnable(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid body, got %d", rec.Code)
		}
	}

	// 2. Overly long TOTP code returns 400.
	{
		rec := httptest.NewRecorder()
		longCode := strings.Repeat("1", maxTOTPCodeLen+1)
		req := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/enable",
			strings.NewReader(`{"code":"`+longCode+`"}`))
		ctx := auth.ContextWithUser(context.Background(), &models.User{ID: 1, Username: "testuser"})
		req = req.WithContext(ctx)

		ah.TOTPEnable(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid TOTP code length, got %d", rec.Code)
		}
	}
}
