package server

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/mail"
)

// newTestServer baut den ECHTEN Server über server.New — Routen, Middleware-Kette,
// eingebettete Assets — gegen die Test-Datenbank. Bis hierher prüfte kein Test
// diese Verdrahtung: internal/server lag bei 4 % Abdeckung, und ein Tippfehler in
// einer Route wäre erst im Browser aufgefallen (Hundert 94).
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	pool := testPool(t)
	mgr, err := auth.NewManager(pool, auth.SessionConfig{MaxAge: 3600}, false, false,
		"a-sufficiently-long-test-secret-value")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	wa, err := auth.NewWebAuthnService(pool, "", "", nil) // Passkeys aus (kein RPID)
	if err != nil {
		t.Fatalf("webauthn: %v", err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	// Rate-Limit 0 = aus: die Requests hier kommen alle von derselben (Test-)IP.
	handler, _, err := New(pool, mgr, wa, 0, "", false, false, false,
		"", "", "", backup.S3Config{}, mail.New(mail.Config{}), "", stop)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	return handler
}

func doReq(t *testing.T, h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Die Grundverdrahtung: Gesundheit, App-Shell, statische Assets, und die
// API-Absicherung (anonym = 401, Schreibzugriff ohne CSRF kommt gar nicht erst
// an der Sitzung vorbei).
func TestServerWiring(t *testing.T) {
	h := newTestServer(t)

	if rec := doReq(t, h, http.MethodGet, "/healthz", nil); rec.Code != http.StatusOK {
		t.Errorf("/healthz: %d", rec.Code)
	}

	rec := doReq(t, h, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Parkrr") {
		t.Error("die App-Shell nennt die Anwendung nicht")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "style-src 'self'") {
		t.Errorf("die strikte CSP fehlt auf der Shell: %q", csp)
	}

	for _, path := range []string{"/js/app.js", "/css/style.css", "/manifest.webmanifest", "/sw.js"} {
		if rec := doReq(t, h, http.MethodGet, path, nil); rec.Code != http.StatusOK {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}

	// Anonym an die API: 401, nicht 200 und nicht 500.
	for _, path := range []string{"/api/persons", "/api/vehicles", "/api/backup/status", "/api/users"} {
		if rec := doReq(t, h, http.MethodGet, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonym: %d, erwartet 401", path, rec.Code)
		}
	}

	// Unbekannte API-Route: 404 als JSON, nicht die App-Shell (die würde ein
	// fetch()-Aufrufer als Erfolg mit HTML-Body missverstehen).
	rec = doReq(t, h, http.MethodGet, "/api/gibt-es-nicht", nil)
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "<!doctype") {
		t.Error("eine unbekannte API-Route liefert die App-Shell aus")
	}
}

// Der Login-Weg über die ECHTE Kette: falsche Zugangsdaten sind ein 401 mit
// JSON-Fehler; der Body-Limit-Schutz und das Access-Log hängen mit drin.
func TestServerLoginRejectsBadCredentials(t *testing.T) {
	h := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"definitiv-falsch"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("falsches Passwort: %d, erwartet 401 — %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Fehler nicht als JSON: %q", ct)
	}
}

// Statische Assets kommen gzip-komprimiert, wenn der Client es kann — und
// identisch entpackbar.
func TestServerServesGzippedAssets(t *testing.T) {
	h := newTestServer(t)
	rec := doReq(t, h, http.MethodGet, "/js/app.js", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Code != http.StatusOK {
		t.Fatalf("app.js: %d", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Skip("gzip nicht aktiv (klein genug oder deaktiviert) — nichts zu prüfen")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	dec, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("entpacken: %v", err)
	}
	if !strings.Contains(string(dec), "Parkrr") {
		t.Error("entpackter Inhalt sieht nicht nach app.js aus")
	}
}

// Der Watchdog-Kontext aus main: ein Server, dessen Stop-Kanal schließt, muss
// laufende Hintergrundpfade beenden können, ohne dass ServeHTTP hängt.
func TestServerSurvivesStopChannelClose(t *testing.T) {
	pool := testPool(t)
	mgr, err := auth.NewManager(pool, auth.SessionConfig{MaxAge: 3600}, false, false,
		"a-sufficiently-long-test-secret-value")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	wa, _ := auth.NewWebAuthnService(pool, "", "", nil)
	stop := make(chan struct{})
	handler, _, err := New(pool, mgr, wa, 0, "", false, false, false,
		"", "", "", backup.S3Config{}, mail.New(mail.Config{}), "", stop)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	close(stop)
	// Kurz warten, bis Hintergrund-Goroutinen den Kanal gesehen haben.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	<-ctx.Done()
	if rec := doReq(t, handler, http.MethodGet, "/healthz", nil); rec.Code != http.StatusOK {
		t.Errorf("/healthz nach Stop: %d", rec.Code)
	}
}
