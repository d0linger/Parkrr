package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
