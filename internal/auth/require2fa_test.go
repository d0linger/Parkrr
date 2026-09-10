package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Database tests follow the repository's opt-in DSN convention.
func TestRequire2FASessionAssurance(t *testing.T) {
	for _, factor := range []string{"totp", "passkey"} {
		t.Run(factor, func(t *testing.T) {
			m, pool := testAuthManager(t)
			id := mkAuthUser(t, pool, "editor", false)
			cookies := sessionCookies(t, m, id)
			handler := m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			get := func(path string, want int, reason string) {
				t.Helper()
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, withCookies(httptest.NewRequest(http.MethodGet, path, nil), cookies))
				if rec.Code != want || !strings.Contains(rec.Body.String(), reason) {
					t.Fatalf("%s: got %d %s, want %d %s", path, rec.Code, rec.Body.String(), want, reason)
				}
			}
			get("/api/persons", http.StatusOK, "")
			m.SetRequire2FA(true)
			get("/api/persons", http.StatusForbidden, "2fa_enrollment_required")
			get("/api/auth/me", http.StatusOK, "")
			get("/api/auth/2fa/setup", http.StatusOK, "")
			get("/api/passkeys", http.StatusOK, "")

			if factor == "totp" {
				if _, err := pool.Exec(t.Context(), `UPDATE users SET totp_enabled=true WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := pool.Exec(t.Context(),
					`INSERT INTO webauthn_credentials(user_id, credential_id, public_key, name) VALUES($1,$2,$3,'test')`,
					id, []byte(t.Name()), []byte("key")); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{"/api/persons", "/api/auth/me", "/api/passkeys/register/begin", "/api/auth/2fa/setup"} {
				get(path, http.StatusForbidden, "2fa_authentication_required")
			}
			get("/api/auth/logout", http.StatusOK, "")

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if err := m.CreateVerifiedSession(t.Context(), rec, req, id); err != nil {
				t.Fatal(err)
			}
			cookies = rec.Result().Cookies()
			get("/api/persons", http.StatusOK, "")
			get("/api/auth/me", http.StatusOK, "")

			// Removing the last factor restricts even a formerly verified session.
			if _, err := pool.Exec(t.Context(), `UPDATE users SET totp_enabled=false WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `DELETE FROM webauthn_credentials WHERE user_id=$1`, id); err != nil {
				t.Fatal(err)
			}
			get("/api/persons", http.StatusForbidden, "2fa_enrollment_required")
		})
	}
}
