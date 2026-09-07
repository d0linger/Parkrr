package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Die 2FA-Pflicht (Hundert 41) ist ein Opt-in: aus = unverändertes Verhalten,
// an = ein Konto ohne TOTP und ohne Passkey erreicht nur noch die Einrichtung.
func TestRequire2FABlocksUnenrolledAccounts(t *testing.T) {
	m, pool := testAuthManager(t)
	id := mkAuthUser(t, pool, "editor", false)
	cookies := sessionCookies(t, m, id)

	handler := m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, withCookies(httptest.NewRequest(http.MethodGet, path, nil), cookies))
		return rec
	}

	// Pflicht AUS (Default): alles offen — die Verhaltensneutralität, auf der die
	// Einführung beruht.
	if rec := get("/api/persons"); rec.Code != http.StatusOK {
		t.Fatalf("ohne Pflicht: %d", rec.Code)
	}

	m.SetRequire2FA(true)
	t.Cleanup(func() { m.SetRequire2FA(false) })

	// Gesperrt — mit maschinenlesbarem Grund, damit die Oberfläche zur Einrichtung
	// führen kann statt einen nackten 403 zu zeigen.
	rec := get("/api/persons")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ohne 2FA: %d, erwartet 403", rec.Code)
	}
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error != "2fa_enrollment_required" {
		t.Errorf("Grund %q — die Oberfläche braucht den maschinenlesbaren Wert", body.Error)
	}

	// Die Einrichtungswege bleiben offen: 2FA-Setup, Passkey-Registrierung,
	// Abmelden, eigener Zustand. Sonst könnte niemand die Pflicht je erfüllen.
	for _, path := range []string{"/api/auth/me", "/api/auth/2fa/setup", "/api/passkeys", "/api/auth/logout"} {
		if rec := get(path); rec.Code != http.StatusOK {
			t.Errorf("%s muss ohne 2FA erreichbar bleiben, war %d", path, rec.Code)
		}
	}

	// Mit aktiviertem TOTP fällt die Sperre.
	if _, err := pool.Exec(context.Background(),
		`UPDATE users SET totp_enabled=true, totp_secret='s' WHERE id=$1`, id); err != nil {
		t.Fatalf("enable totp: %v", err)
	}
	if rec := get("/api/persons"); rec.Code != http.StatusOK {
		t.Errorf("mit TOTP: %d, erwartet 200", rec.Code)
	}
}

// Ein Passkey erfüllt die Pflicht genauso wie TOTP — wer nur mit Passkey arbeitet,
// darf nicht in die TOTP-Einrichtung gezwungen werden.
func TestRequire2FAAcceptsPasskeyAsSecondFactor(t *testing.T) {
	m, pool := testAuthManager(t)
	id := mkAuthUser(t, pool, "editor", false)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO webauthn_credentials (user_id, credential_id, public_key, name)
		 VALUES ($1, $2, $3, '2FA-Test-Key')`, id, []byte("cred-2fa-"+t.Name()), []byte("pk")); err != nil {
		t.Fatalf("insert passkey: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM webauthn_credentials WHERE user_id=$1`, id) })
	cookies := sessionCookies(t, m, id)

	m.SetRequire2FA(true)
	t.Cleanup(func() { m.SetRequire2FA(false) })

	handler := m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, withCookies(httptest.NewRequest(http.MethodGet, "/api/persons", nil), cookies))
	if rec.Code != http.StatusOK {
		t.Errorf("mit Passkey: %d, erwartet 200 — %s", rec.Code, rec.Body.String())
	}
}
