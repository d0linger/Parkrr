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

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// TestPasskeyLoginThrottle verifies the usernameless passkey login throttle
// locks a client IP after the configured number of failed attempts and reports
// a 429 with a Retry-After header, mirroring the password-login lockout.
func TestPasskeyLoginThrottle(t *testing.T) {
	ah := &AuthHandler{
		Handler: &Handler{},
		Auth:    &auth.Manager{}, // trustProxy=false -> ClientIP uses RemoteAddr
		Limiter: auth.NewLoginLimiter(3, time.Minute, time.Minute),
	}
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/finish", nil)
		r.RemoteAddr = "203.0.113.7:52000"
		return r
	}

	// First attempt is allowed and yields the IP-scoped key.
	key, ok := ah.throttlePasskeyLogin(httptest.NewRecorder(), req())
	if !ok {
		t.Fatal("first attempt should be allowed")
	}
	if key != "passkey|203.0.113.7" {
		t.Fatalf("unexpected throttle key %q", key)
	}

	// Trip the lock with the configured number of failures.
	for i := 0; i < 3; i++ {
		ah.Limiter.RecordFailure(key)
	}

	rec := httptest.NewRecorder()
	_, ok = ah.throttlePasskeyLogin(rec, req())
	if ok {
		t.Fatal("attempt after lock should be blocked")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked attempt: got status %d want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("blocked attempt should set Retry-After")
	}

	// A different IP is unaffected by the first IP's lock.
	other := httptest.NewRequest(http.MethodPost, "/", nil)
	other.RemoteAddr = "198.51.100.9:40000"
	if _, ok := ah.throttlePasskeyLogin(httptest.NewRecorder(), other); !ok {
		t.Error("a different client IP must not be throttled")
	}
}

func TestPasskeyRegisterBegin_NameLength(t *testing.T) {
	wa, err := auth.NewWebAuthnService(nil, "example.com", "Example", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("failed to create webauthn service: %v", err)
	}

	ah := &AuthHandler{
		Handler:  &Handler{},
		WebAuthn: wa,
	}

	// Create request with extremely long name
	body := map[string]string{
		"name": strings.Repeat("a", maxNameLen+1),
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/passkeys/register/begin", bytes.NewReader(b))
	w := httptest.NewRecorder()

	ah.PasskeyRegisterBegin(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp["error"] != "name is too long" {
		t.Errorf("got error %q, want %q", resp["error"], "name is too long")
	}
}

func TestPasskeyRegisterFinish_PerAccountRateLimit(t *testing.T) {
	ah := &AuthHandler{
		Handler:     &Handler{},
		Auth:        &auth.Manager{},
		Limiter:     auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		UserLimiter: auth.NewStickyLoginLimiter(3, time.Minute, time.Minute),
	}

	reqFrom := func(ip string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/passkeys/register/finish", nil)
		r.RemoteAddr = ip + ":1234"
		return r
	}

	// Record 3 registration failures for user "victim" across 3 different IPs.
	for i, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		key, cip, ok := ah.checkRateLimit(httptest.NewRecorder(), reqFrom(ip), "victim")
		if !ok {
			t.Fatalf("attempt %d from %s should be allowed", i, ip)
		}
		ah.recordReauthFailure(key, cip)
	}

	// The 4th attempt from a fresh IP must be rate-limited per-account.
	rec := httptest.NewRecorder()
	if _, _, ok := ah.checkRateLimit(rec, reqFrom("4.4.4.4"), "victim"); ok {
		t.Fatal("per-account lockout should trip after registration failures across rotating IPs")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 from per-account throttle, got %d", rec.Code)
	}
}

// TestPasskeyRegisterBegin_RateLimitWithOpenStepUpWindow deckt die Luecke ab, die
// den checkRateLimit-Aufruf in PasskeyRegisterBegin ueberhaupt noetig macht.
//
// requireStepUp drosselt selbst — aber NUR auf dem Passwort-Zweig. Ist die
// Anmeldung frisch genug (stepUpWindow), kehrt es sofort mit true zurueck und
// beruehrt den Begrenzer nie. Genau dieses offene Fenster wird hier hergestellt:
// eine echte Sitzung mit created_at = jetzt. Ohne den Aufruf in
// PasskeyRegisterBegin laeuft die Anfrage bis zur Zeremonie-Erzeugung durch und
// antwortet NICHT mit 429 — der Test faellt dann um. Er haelt also wirklich die
// Stelle fest, statt nur zu bestaetigen, dass requireStepUp drosselt.
//
// Laeuft nur mit PARKRR_TEST_DATABASE_URL (testHandler ueberspringt sonst).
func TestPasskeyRegisterBegin_RateLimitWithOpenStepUpWindow(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	mgr, err := auth.NewManager(h.Pool, auth.SessionConfig{MaxAge: 3600}, false, false, "a-sufficiently-long-test-secret")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	wa, err := auth.NewWebAuthnService(h.Pool, "example.com", "Example", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("NewWebAuthnService: %v", err)
	}
	ah := &AuthHandler{
		Handler:     h,
		Auth:        mgr,
		WebAuthn:    wa,
		Limiter:     auth.NewLoginLimiter(3, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		UserLimiter: auth.NewStickyLoginLimiter(1000, time.Minute, time.Minute),
	}

	const uname = "passkey-begin-throttle-Integration"
	hash, err := auth.HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var uid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1,$2) RETURNING id`, uname, hash).Scan(&uid); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, uname) })

	// Echte Sitzung, created_at = jetzt => das Step-up-Fenster steht offen und
	// requireStepUp wird ohne Passwort und ohne Drosselung durchwinken.
	rec := httptest.NewRecorder()
	if err := mgr.CreateSession(ctx, rec, httptest.NewRequest(http.MethodPost, "/login", nil), uid); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie created")
	}

	const ip = "192.0.2.1"
	u := &models.User{ID: uid, Username: uname}
	reqFrom := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/passkeys/register/begin",
			bytes.NewReader([]byte(`{"name":"key1"}`)))
		r.RemoteAddr = ip + ":1234"
		r.AddCookie(sessionCookie)
		return r.WithContext(auth.ContextWithUser(ctx, u))
	}

	// Vorbedingung: bei offenem Fenster kommt requireStepUp ohne Passwort durch.
	// Faellt das um, prueft der Rest des Tests nicht mehr, was er soll.
	if w := httptest.NewRecorder(); !ah.requireStepUp(w, reqFrom(), uname, "") {
		t.Fatalf("setup: recent session should pass step-up without a password (got %d)", w.Code)
	}

	// Begrenzer erschoepfen (3 Fehlversuche => gesperrt).
	for i := 0; i < 3; i++ {
		key, cip, ok := ah.checkRateLimit(httptest.NewRecorder(), reqFrom(), uname)
		if !ok {
			t.Fatalf("attempt %d should still be allowed", i)
		}
		ah.recordReauthFailure(key, cip)
	}

	// Trotz offenem Step-up-Fenster muss der Endpunkt jetzt 429 liefern.
	w := httptest.NewRecorder()
	ah.PasskeyRegisterBegin(w, reqFrom())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests despite the open step-up window, got %d: %s",
			w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("throttled response should carry a Retry-After header")
	}
}
