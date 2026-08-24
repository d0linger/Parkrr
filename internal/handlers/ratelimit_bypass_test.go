package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// Usernames are matched case-insensitively in the database, so the login
// throttle must treat "admin" and "Admin" as the same key — otherwise varying
// the casing hands the attacker a fresh rate-limit bucket per variant.
func TestRateLimitBypass_Casing(t *testing.T) {
	ah := &AuthHandler{
		Handler:     &Handler{},
		Auth:        &auth.Manager{}, // trustProxy=false -> ClientIP uses RemoteAddr
		Limiter:     auth.NewLoginLimiter(2, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(20, time.Minute, time.Minute),
		UserLimiter: auth.NewLoginLimiter(1000, time.Minute, time.Minute),
	}

	ip := "1.2.3.4"
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = ip + ":1234"
		return r
	}

	key1, _, ok := ah.checkRateLimit(httptest.NewRecorder(), req(), "admin")
	if !ok {
		t.Fatal("first attempt should be allowed")
	}

	// Lock "admin" for this IP.
	ah.Limiter.RecordFailure(key1)
	ah.Limiter.RecordFailure(key1)

	rec1 := httptest.NewRecorder()
	if _, _, ok = ah.checkRateLimit(rec1, req(), "admin"); ok {
		t.Fatal("admin should be blocked after 2 failures")
	}
	if rec1.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for admin, got %d", rec1.Code)
	}

	// The same account under different casing must share the lockout.
	if _, _, ok = ah.checkRateLimit(httptest.NewRecorder(), req(), "Admin"); ok {
		t.Error("casing bypass: 'Admin' was allowed while 'admin' was locked")
	}
}

// Password spraying — one guess against many usernames from a single IP — must
// trip the per-IP throttle even though no single username+IP key reaches its
// own threshold.
func TestLoginSprayingTrippedPerIP(t *testing.T) {
	ah := &AuthHandler{
		Handler:     &Handler{},
		Auth:        &auth.Manager{}, // trustProxy=false -> ClientIP uses RemoteAddr
		Limiter:     auth.NewLoginLimiter(5, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(3, time.Minute, time.Minute),
		UserLimiter: auth.NewLoginLimiter(1000, time.Minute, time.Minute),
	}
	ip := "9.9.9.9"
	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = ip + ":5555"
		return r
	}

	// Three failures, each against a different username (so each username+IP key
	// stays at one failure, well under its own limit of 5). The IP is not blocked
	// yet, so ipThrottled passes.
	for i, user := range []string{"alice", "bob", "carol"} {
		if ah.ipThrottled(httptest.NewRecorder(), req()) {
			t.Fatalf("attempt %d should not be IP-blocked yet", i)
		}
		key, cip, ok := ah.checkRateLimit(httptest.NewRecorder(), req(), user)
		if !ok {
			t.Fatalf("attempt %d for %q should be allowed", i, user)
		}
		ah.recordLoginFailure(key, cip)
	}

	// A fourth attempt from the same IP is now blocked by the per-IP throttle,
	// regardless of the (fresh) username.
	rec := httptest.NewRecorder()
	if !ah.ipThrottled(rec, req()) {
		t.Fatal("spraying: the IP should be locked after 3 failures across usernames")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 from the IP throttle, got %d", rec.Code)
	}
}

// Resetting the per-account limiter on a successful login must NOT clear the
// per-IP spray counter — otherwise one legit login from a shared egress IP would
// hand a co-located sprayer a fresh budget.
func TestSuccessfulLoginDoesNotResetIPThrottle(t *testing.T) {
	ah := &AuthHandler{
		Handler:     &Handler{},
		Auth:        &auth.Manager{},
		Limiter:     auth.NewLoginLimiter(5, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(3, time.Minute, time.Minute),
		UserLimiter: auth.NewLoginLimiter(1000, time.Minute, time.Minute),
	}
	ip := "7.7.7.7"
	key := "someuser|" + ip
	for i := 0; i < 3; i++ {
		ah.recordLoginFailure(key, ip)
	}
	if allowed, _ := ah.IPLimiter.Allowed(ip); allowed {
		t.Fatal("IP should be locked after 3 failures")
	}
	// A successful login resets the per-account key (the same key that accrued
	// the failures above) — the per-IP counter must stay untouched.
	ah.Limiter.Reset(key)
	if allowed, _ := ah.IPLimiter.Allowed(ip); allowed {
		t.Error("resetting the per-account limiter must not unlock the per-IP throttle")
	}
}

// Finding P-02: the authenticated re-auth endpoints (2FA enable/disable,
// backup-code regeneration) must record a failed attempt against the per-account
// limiter too, not only username|ip — otherwise a distributed brute force that
// rotates its source IP keeps every username|ip key at one failure and never
// trips the per-account lockout that checkRateLimit already enforces.
// recordReauthFailure (used by those handlers) feeds both counters.
func TestReauthFailureTripsPerAccountAcrossIPs(t *testing.T) {
	ah := &AuthHandler{
		Handler: &Handler{},
		Auth:    &auth.Manager{}, // trustProxy=false -> ClientIP uses RemoteAddr
		// username|ip and per-IP effectively unlimited, so only the per-account
		// (IP-independent) counter can accumulate across rotating IPs.
		Limiter:     auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		UserLimiter: auth.NewStickyLoginLimiter(3, time.Minute, time.Minute),
	}
	reqFrom := func(ip string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/disable", nil)
		r.RemoteAddr = ip + ":1234"
		return r
	}
	// Three failed re-auths for the same account, each from a DIFFERENT IP.
	for i, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		key, cip, ok := ah.checkRateLimit(httptest.NewRecorder(), reqFrom(ip), "victim")
		if !ok {
			t.Fatalf("attempt %d from %s should be allowed", i, ip)
		}
		ah.recordReauthFailure(key, cip)
	}
	// A fourth attempt from yet another fresh IP must now be blocked per-account.
	rec := httptest.NewRecorder()
	if _, _, ok := ah.checkRateLimit(rec, reqFrom("4.4.4.4"), "victim"); ok {
		t.Fatal("P-02: per-account lockout should trip after failures across rotating IPs")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 from the per-account throttle, got %d", rec.Code)
	}
}

// PR #125: Ein falsches aktuelles Passwort bei ChangePassword ist ein Re-Auth-
// Fehlversuch, kein Login — recordReauthFailure darf die login-only Pro-IP-
// Drossel (IPLimiter) NICHT fuellen. Sonst koennte ein vertippter Nutzer den
// *Login* fuer sein ganzes NAT sperren. Das ist das eine Verhalten, das den Fix
// vom alten recordLoginFailure unterscheidet (das den IPLimiter fuettert): ein
// Rueckbau wuerde ipThrottled hier ausloesen und den Test brechen. Die Konto-
// Drossel ueber rotierende IPs deckt bereits TestReauthFailureTripsPerAccountAcrossIPs.
func TestChangePasswordFailureDoesNotTripLoginIPThrottle(t *testing.T) {
	ah := &AuthHandler{
		Handler: &Handler{},
		Auth:    &auth.Manager{}, // trustProxy=false -> ClientIP uses RemoteAddr
		// Global + per-account effektiv unbegrenzt; nur die Pro-IP-Login-Drossel
		// ist eng, damit die Zusicherung isoliert, ob genau sie gefuettert wird.
		Limiter:     auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		IPLimiter:   auth.NewLoginLimiter(3, time.Minute, time.Minute),
		UserLimiter: auth.NewStickyLoginLimiter(1000, time.Minute, time.Minute),
	}
	reqFromIP := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", nil)
		r.RemoteAddr = "9.9.9.9:1234"
		return r
	}
	// Fuenf Fehlversuche von EINER IP — deutlich ueber der Pro-IP-Schwelle von 3.
	// Als Re-Auth-Fehlversuche muessen sie den IPLimiter unberuehrt lassen.
	for i := 0; i < 5; i++ {
		key, ip, ok := ah.checkRateLimit(httptest.NewRecorder(), reqFromIP(), "user1")
		if !ok {
			t.Fatalf("attempt %d should pass the ChangePassword gate", i)
		}
		ah.recordReauthFailure(key, ip)
	}
	// Die login-only Pro-IP-Drossel muss diese IP weiter zulassen; recordLoginFailure
	// haette sie nach 3 ausgeloest — das bewacht gegen einen Rueckbau des Fixes.
	if ah.ipThrottled(httptest.NewRecorder(), reqFromIP()) {
		t.Fatal("ChangePassword re-auth failures must not trip the login per-IP throttle")
	}
}
