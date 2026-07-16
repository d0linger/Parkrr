package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/preining/parkrr/internal/auth"
)

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

func TestExtraInputLengthValidation(t *testing.T) {
	h := &Handler{}

	wa, err := auth.NewWebAuthnService(nil, "localhost", "Test", []string{"http://localhost"})
	if err != nil {
		t.Fatalf("failed to build webauthn: %v", err)
	}
	ah := &AuthHandler{
		Handler:  h,
		WebAuthn: wa,
	}

	t.Run("PasskeyRegisterBegin: Name too long", func(t *testing.T) {
		body := struct {
			Name string `json:"name"`
		}{
			Name: strings.Repeat("a", maxNameLen+1),
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/passkeys/register/begin", bytes.NewReader(b))
		w := httptest.NewRecorder()

		ah.PasskeyRegisterBegin(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !strings.Contains(resp["error"], "passkey name is too long") {
			t.Errorf("unexpected error message: %q", resp["error"])
		}
	})

	t.Run("ChangeVehicleStatus: Note too long", func(t *testing.T) {
		body := struct {
			Status string `json:"status"`
			Note   string `json:"note"`
		}{
			Status: "stored",
			Note:   strings.Repeat("a", maxNoteLen+1),
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/vehicles/1/status", bytes.NewReader(b))
		req.SetPathValue("id", "1")
		w := httptest.NewRecorder()

		h.ChangeVehicleStatus(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !strings.Contains(resp["error"], "note is too long") {
			t.Errorf("unexpected error message: %q", resp["error"])
		}
	})

	t.Run("CreateRecurringCharge: Description too long", func(t *testing.T) {
		body := struct {
			Description string `json:"description"`
		}{
			Description: strings.Repeat("a", maxNameLen+1),
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/persons/1/recurring", bytes.NewReader(b))
		req.SetPathValue("id", "1")
		w := httptest.NewRecorder()

		h.CreateRecurringCharge(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !strings.Contains(resp["error"], "description is too long") {
			t.Errorf("unexpected error message: %q", resp["error"])
		}
	})

	t.Run("CreateUser: Email too long", func(t *testing.T) {
		body := struct {
			Username string `json:"username"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}{
			Username: "valid_user",
			Email:    strings.Repeat("b", maxEmailLen+1),
			Password: "valid_password",
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/users", bytes.NewReader(b))
		w := httptest.NewRecorder()

		h.CreateUser(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !strings.Contains(resp["error"], "email is too long") {
			t.Errorf("unexpected error message: %q", resp["error"])
		}
	})

	t.Run("UpdateUser: Email too long", func(t *testing.T) {
		body := struct {
			Username string `json:"username"`
			Email    string `json:"email"`
		}{
			Username: "valid_user",
			Email:    strings.Repeat("b", maxEmailLen+1),
		}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("PUT", "/api/users/1", bytes.NewReader(b))
		req.SetPathValue("id", "1")
		w := httptest.NewRecorder()

		h.UpdateUser(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !strings.Contains(resp["error"], "email is too long") {
			t.Errorf("unexpected error message: %q", resp["error"])
		}
	})
}
