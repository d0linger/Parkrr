package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/preining/parkrr/internal/auth"
)

func TestLoginRecordsVerifiedFactor(t *testing.T) {
	for _, tc := range []struct {
		name            string
		factor          string
		loginStatus     int
		protectedStatus int
		verified        bool
	}{
		{name: "password with passkey enrolled", factor: "passkey", loginStatus: 200, protectedStatus: 403},
		{name: "totp proof", factor: "totp", loginStatus: 200, protectedStatus: 200, verified: true},
		{name: "recovery proof", factor: "recovery", loginStatus: 200, protectedStatus: 200, verified: true},
		{name: "missing totp proof", factor: "missing", loginStatus: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler(t)
			mgr, err := auth.NewManager(h.Pool, auth.SessionConfig{MaxAge: 3600}, false, false,
				"a-sufficiently-long-test-secret")
			if err != nil {
				t.Fatal(err)
			}
			mgr.SetRequire2FA(true)
			stop := make(chan struct{})
			defer close(stop)
			ah := NewAuthHandler(h, mgr, nil, stop)
			const password = "synthetic-login-password"
			hash, err := auth.HashPassword(password)
			if err != nil {
				t.Fatal(err)
			}
			username := "mfa-proof-" + tc.factor
			var id int64
			if err := h.Pool.QueryRow(t.Context(),
				`INSERT INTO users(username,password_hash) VALUES($1,$2) RETURNING id`, username, hash).Scan(&id); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := h.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id); err != nil {
					t.Error(err)
				}
			})
			var code string
			if tc.factor == "passkey" {
				if _, err := h.Pool.Exec(t.Context(),
					`INSERT INTO webauthn_credentials(user_id,credential_id,public_key,name) VALUES($1,$2,$3,'test')`,
					id, []byte(username), []byte("key")); err != nil {
					t.Fatal(err)
				}
			} else {
				key, err := auth.GenerateTOTP(username)
				if err != nil {
					t.Fatal(err)
				}
				encrypted, err := mgr.EncryptTOTPSecret(key.Secret())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := h.Pool.Exec(t.Context(), `UPDATE users SET totp_enabled=true,totp_secret=$1 WHERE id=$2`,
					encrypted, id); err != nil {
					t.Fatal(err)
				}
				switch tc.factor {
				case "totp":
					code, err = totp.GenerateCode(key.Secret(), time.Now())
					if err != nil {
						t.Fatal(err)
					}
				case "recovery":
					codes, err := mgr.GenerateBackupCodes(t.Context(), id)
					if err != nil {
						t.Fatal(err)
					}
					code = codes[0]
				}
			}
			payload, err := json.Marshal(loginRequest{Username: username, Password: password, TOTPCode: code})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			ah.Login(rec, req)
			if rec.Code != tc.loginStatus {
				t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
			}
			if tc.loginStatus != http.StatusOK {
				if len(rec.Result().Cookies()) != 0 {
					t.Fatal("failed login issued cookies")
				}
				return
			}
			var verified bool
			if err := h.Pool.QueryRow(t.Context(),
				`SELECT factor_verified FROM sessions WHERE user_id=$1`, id).Scan(&verified); err != nil {
				t.Fatal(err)
			}
			if verified != tc.verified {
				t.Fatalf("verified=%v, want %v", verified, tc.verified)
			}
			protected := httptest.NewRequest(http.MethodGet, "/api/persons", nil)
			for _, cookie := range rec.Result().Cookies() {
				protected.AddCookie(cookie)
			}
			got := httptest.NewRecorder()
			mgr.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(got, protected)
			if got.Code != tc.protectedStatus {
				t.Fatalf("protected: %d %s", got.Code, got.Body.String())
			}
		})
	}
}

// Passkey-only (Hundert 42): der Passwort-Endpunkt ist abgeschaltet — für JEDEN,
// der ihn direkt anspricht, nicht nur für die (ohnehin ausgeblendete) Maske. Der
// Grund ist maschinenlesbar, damit die Oberfläche ihn erkennen kann.
func TestPasskeyOnlyDisablesPasswordLogin(t *testing.T) {
	ah := &AuthHandler{Handler: &Handler{PasskeyOnly: true}}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"egal"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ah.Login(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Passwort-Login im Passkey-only-Modus: %d, erwartet 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "password_login_disabled") {
		t.Errorf("Grund fehlt: %s", rec.Body.String())
	}
}

// Die Fähigkeiten-Antwort trägt den Modus, damit die Anmeldemaske den
// Passwortteil gar nicht erst zeigt.
func TestCapabilitiesReportPasskeyOnly(t *testing.T) {
	ah := &AuthHandler{Handler: &Handler{PasskeyOnly: true}}
	rec := httptest.NewRecorder()
	ah.Capabilities(rec, httptest.NewRequest(http.MethodGet, "/api/auth/capabilities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("capabilities: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"passkey_only":true`) {
		t.Errorf("passkey_only fehlt: %s", rec.Body.String())
	}
}
