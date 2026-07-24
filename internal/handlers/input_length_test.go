package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

func floatPtr(f float64) *float64 { return &f }

func TestInputLengthValidation(t *testing.T) {
	h := &Handler{}

	longName := strings.Repeat("a", maxNameLen+1)
	longEmail := strings.Repeat("b", maxEmailLen+1)
	longNote := strings.Repeat("c", maxNoteLen+1)
	longPhone := strings.Repeat("d", maxPhoneLen+1)
	longAddress := strings.Repeat("e", maxAddressLen+1)

	tests := []struct {
		name       string
		path       string
		method     string
		body       any
		wantStatus int
		errMsg     string
	}{
		{
			name:       "CreatePerson: First Name too long",
			path:       "/api/persons",
			method:     "POST",
			body:       personRequest{FirstName: longName},
			wantStatus: http.StatusBadRequest,
			errMsg:     "name is too long",
		},
		{
			name:       "CreatePerson: Last Name too long",
			path:       "/api/persons",
			method:     "POST",
			body:       personRequest{LastName: longName},
			wantStatus: http.StatusBadRequest,
			errMsg:     "name is too long",
		},
		{
			name:       "CreatePerson: Email too long",
			path:       "/api/persons",
			method:     "POST",
			body:       personRequest{FirstName: "John", Email: longEmail},
			wantStatus: http.StatusBadRequest,
			errMsg:     "email is too long",
		},
		{
			name:       "CreatePerson: Phone too long",
			path:       "/api/persons",
			method:     "POST",
			body:       personRequest{FirstName: "John", Phone: longPhone},
			wantStatus: http.StatusBadRequest,
			errMsg:     "phone is too long",
		},
		{
			name:       "CreatePerson: Address too long",
			path:       "/api/persons",
			method:     "POST",
			body:       personRequest{FirstName: "John", Address: longAddress},
			wantStatus: http.StatusBadRequest,
			errMsg:     "address is too long",
		},
		{
			name:       "CreatePerson: Note too long",
			path:       "/api/persons",
			method:     "POST",
			body:       personRequest{FirstName: "John", Notes: longNote},
			wantStatus: http.StatusBadRequest,
			errMsg:     "notes is too long",
		},
		{
			name:       "CreateCategory: Name too long",
			path:       "/api/categories",
			method:     "POST",
			body:       categoryRequest{Name: longName},
			wantStatus: http.StatusBadRequest,
			errMsg:     "name is too long",
		},
		{
			name:       "CreateServiceType: Name too long",
			path:       "/api/services",
			method:     "POST",
			body:       serviceTypeRequest{Name: longName},
			wantStatus: http.StatusBadRequest,
			errMsg:     "name is too long",
		},
		{
			name:       "CreateCharge: Description too long",
			path:       "/api/charges",
			method:     "POST",
			body:       chargeRequest{PersonID: 1, Description: longName},
			wantStatus: http.StatusBadRequest,
			errMsg:     "description is too long",
		},
		{
			name:       "CreateAgreement: Inline vehicle label too long",
			path:       "/api/persons/1/agreements",
			method:     "POST",
			body:       agreementRequest{Amount: floatPtr(100), Period: "monthly", StartDate: "2026-01-01", NewVehicles: []newVehicleReq{{CategoryID: 1, Label: longName}}},
			wantStatus: http.StatusBadRequest,
			errMsg:     "label is too long",
		},
		{
			name:       "CreateAgreement: Inline vehicle plate too long",
			path:       "/api/persons/1/agreements",
			method:     "POST",
			body:       agreementRequest{Amount: floatPtr(100), Period: "monthly", StartDate: "2026-01-01", NewVehicles: []newVehicleReq{{CategoryID: 1, LicensePlate: longName}}},
			wantStatus: http.StatusBadRequest,
			errMsg:     "license_plate is too long",
		},
		{
			name:       "CreateAgreement: Edit vehicle label too long",
			path:       "/api/persons/1/agreements",
			method:     "POST",
			body:       agreementRequest{Amount: floatPtr(100), Period: "monthly", StartDate: "2026-01-01", EditVehicles: []editVehicleReq{{ID: 1, CategoryID: 1, Label: longName}}},
			wantStatus: http.StatusBadRequest,
			errMsg:     "label is too long",
		},
		{
			name:       "CreateAgreement: Edit vehicle plate too long",
			path:       "/api/persons/1/agreements",
			method:     "POST",
			body:       agreementRequest{Amount: floatPtr(100), Period: "monthly", StartDate: "2026-01-01", EditVehicles: []editVehicleReq{{ID: 1, CategoryID: 1, LicensePlate: longName}}},
			wantStatus: http.StatusBadRequest,
			errMsg:     "license_plate is too long",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader(b))
			w := httptest.NewRecorder()

			switch tt.name {
			case "CreatePerson: First Name too long", "CreatePerson: Last Name too long", "CreatePerson: Email too long", "CreatePerson: Phone too long", "CreatePerson: Address too long", "CreatePerson: Note too long":
				h.CreatePerson(w, req)
			case "CreateCategory: Name too long":
				h.CreateCategory(w, req)
			case "CreateServiceType: Name too long":
				h.CreateServiceType(w, req)
			case "CreateCharge: Description too long":
				h.CreateCharge(w, req)
			case "CreateAgreement: Inline vehicle label too long", "CreateAgreement: Inline vehicle plate too long", "CreateAgreement: Edit vehicle label too long", "CreateAgreement: Edit vehicle plate too long":
				req.SetPathValue("id", "1")
				h.CreateAgreement(w, req)
			}

			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, tt.wantStatus)
			}
			var resp map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("failed to unmarshal response: %v", err)
			}
			if !strings.Contains(resp["error"], tt.errMsg) {
				t.Errorf("got error %q, want it to contain %q", resp["error"], tt.errMsg)
			}
		})
	}
}

func TestPasskeyInputLengthValidation(t *testing.T) {
	wa, err := auth.NewWebAuthnService(nil, "localhost", "Parkrr", []string{"http://localhost"})
	if err != nil {
		t.Fatalf("failed to create WebAuthn service: %v", err)
	}
	ah := &AuthHandler{
		Handler:  &Handler{},
		WebAuthn: wa,
	}
	longName := strings.Repeat("a", maxNameLen+1)

	// Create request with user in context because PasskeyRegisterBegin retrieves user from context.
	u := &models.User{Username: "testuser"}
	req := httptest.NewRequest(http.MethodPost, "/api/passkeys/register/begin", strings.NewReader(`{"name":"`+longName+`"}`))
	req = req.WithContext(auth.ContextWithUser(req.Context(), u))

	w := httptest.NewRecorder()
	ah.PasskeyRegisterBegin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("PasskeyRegisterBegin with long name: expected 400, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if !strings.Contains(resp["error"], "passkey name is too long") {
		t.Errorf("expected error containing 'passkey name is too long', got %q", resp["error"])
	}
}
