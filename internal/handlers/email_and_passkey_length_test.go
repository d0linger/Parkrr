package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/preining/parkrr/internal/auth"
)

func TestUserEmailLengthValidation(t *testing.T) {
	h := &Handler{}

	longEmail := strings.Repeat("b", maxEmailLen+1)

	// CreateUser with over-long email
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/users",
		strings.NewReader(`{"username":"validuser","email":"`+longEmail+`","password":"password123","role":"editor"}`))
	h.CreateUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("CreateUser with long email: expected 400, got %d", rec.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !strings.Contains(resp["error"], "email is too long") {
		t.Errorf("expected email length error, got %q", resp["error"])
	}

	// UpdateUser with over-long email
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPut, "/api/users/1",
		strings.NewReader(`{"username":"validuser","email":"`+longEmail+`","role":"editor"}`))
	req2.SetPathValue("id", "1")
	h.UpdateUser(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("UpdateUser with long email: expected 400, got %d", rec2.Code)
	}
	var resp2 map[string]string
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !strings.Contains(resp2["error"], "email is too long") {
		t.Errorf("expected email length error, got %q", resp2["error"])
	}
}

func TestPasskeyNameLengthValidation(t *testing.T) {
	wa, err := auth.NewWebAuthnService(nil, "example.com", "Parkrr", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("failed to create WebAuthnService: %v", err)
	}

	ah := &AuthHandler{
		Handler:  &Handler{},
		WebAuthn: wa,
	}

	longName := strings.Repeat("a", maxNameLen+1)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/passkeys/register/begin",
		strings.NewReader(`{"name":"`+longName+`"}`))

	ah.PasskeyRegisterBegin(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("PasskeyRegisterBegin with long name: expected 400, got %d", rec.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !strings.Contains(resp["error"], "passkey name is too long") {
		t.Errorf("expected passkey name length error, got %q", resp["error"])
	}
}
