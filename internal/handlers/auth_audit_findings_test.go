package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// Regression tests for the auth audit findings AUTH-01 .. AUTH-05.
// All run only with PARKRR_TEST_DATABASE_URL (testHandler skips otherwise).

type authFixture struct {
	h   *Handler
	mgr *auth.Manager
	ah  *AuthHandler
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	h := testHandler(t)
	mgr, err := auth.NewManager(h.Pool, auth.SessionConfig{MaxAge: 3600}, false, false, "a-sufficiently-long-test-secret")
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return &authFixture{h: h, mgr: mgr, ah: &AuthHandler{
		Handler: h,
		Auth:    mgr,
		// Generous in-memory budgets: these tests exercise the persistent
		// counters, not the IP/username throttles.
		Limiter:         auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		IPLimiter:       auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		UserLimiter:     auth.NewLoginLimiter(1000, time.Minute, time.Minute),
		CeremonyLimiter: auth.NewLoginLimiter(1000, time.Minute, time.Minute),
	}}
}

func (f *authFixture) createUser(t *testing.T, name, password string) int64 {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var id int64
	if err := f.h.Pool.QueryRow(context.Background(),
		`INSERT INTO users (username, password_hash) VALUES ($1,$2) RETURNING id`, name, hash).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", name, err)
	}
	t.Cleanup(func() { _, _ = f.h.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id) })
	return id
}

// sessionCookie creates a real session for uid and returns its cookie.
func (f *authFixture) sessionCookie(t *testing.T, uid int64) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := f.mgr.CreateSession(context.Background(), rec, httptest.NewRequest(http.MethodPost, "/login", nil), uid); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func authJSONReq(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	return r
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// AUTH-01: an admin password reset that commits while ChangePassword is in flight
// (here: during the breach lookup) must win. The in-flight change must neither
// overwrite the admin's hash nor leave a session behind.
func TestChangePasswordDoesNotUndoConcurrentReset(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	const oldPW, attackerPW, adminPW = "old-password-horse", "attacker-password-x", "admin-reset-password"
	uid := f.createUser(t, "chpw-race-Integration", oldPW)
	cookie := f.sessionCookie(t, uid)

	adminHash, err := auth.HashPassword(adminPW)
	if err != nil {
		t.Fatal(err)
	}
	// The breach lookup is the window: the admin reset commits inside it.
	f.h.CheckBreachedPasswords = true
	f.h.hibpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if _, err := f.h.Pool.Exec(ctx,
			`UPDATE users SET password_hash=$1 WHERE id=$2`, adminHash, uid); err != nil {
			t.Errorf("simulated admin reset: %v", err)
		}
		if _, err := f.h.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, uid); err != nil {
			t.Errorf("simulated session revocation: %v", err)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}

	r := authJSONReq(t, http.MethodPost, "/api/auth/change-password",
		changePasswordRequest{CurrentPassword: oldPW, NewPassword: attackerPW})
	r.AddCookie(cookie)
	r = r.WithContext(auth.ContextWithUser(ctx, &models.User{ID: uid, Username: "chpw-race-Integration"}))
	w := httptest.NewRecorder()
	f.ah.ChangePassword(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 after a concurrent reset, got %d: %s", w.Code, w.Body.String())
	}
	var stored string
	var sessions int
	if err := f.h.Pool.QueryRow(ctx, `SELECT password_hash, (SELECT count(*) FROM sessions WHERE user_id=$1)
		FROM users WHERE id=$1`, uid).Scan(&stored, &sessions); err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(stored, adminPW) {
		t.Error("the admin's reset password was overwritten by the in-flight change")
	}
	if sessions != 0 {
		t.Errorf("in-flight change left %d session(s) after the reset", sessions)
	}
}

// AUTH-02: usernames are unique regardless of case, both on create and rename,
// and the conflict is a 409.
func TestUsernameUniqueIgnoringCase(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	first := f.createUser(t, "CaseDup-Integration", "some-password-1")
	_ = first

	w := httptest.NewRecorder()
	f.h.CreateUser(w, authJSONReq(t, http.MethodPost, "/api/users",
		userRequest{Username: "casedup-integration", Password: "some-password-2", Role: models.RoleEditor}))
	if w.Code != http.StatusConflict {
		t.Fatalf("create case variant: want 409, got %d: %s", w.Code, w.Body.String())
	}

	other := f.createUser(t, "CaseDup-Other-Integration", "some-password-3")
	r := authJSONReq(t, http.MethodPut, "/api/users/x",
		userRequest{Username: "CASEDUP-INTEGRATION", Role: models.RoleEditor})
	r.SetPathValue("id", strconv.FormatInt(other, 10))
	w = httptest.NewRecorder()
	f.h.UpdateUser(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("rename to case variant: want 409, got %d: %s", w.Code, w.Body.String())
	}

	// Re-auth is bound to the session user's id: another account's password
	// never verifies for this one.
	if _, err := f.mgr.AuthenticateUserID(ctx, other, "some-password-1"); err == nil {
		t.Error("AuthenticateUserID accepted another account's password")
	}
	if _, err := f.mgr.AuthenticateUserID(ctx, other, "some-password-3"); err != nil {
		t.Errorf("AuthenticateUserID rejected the right password: %v", err)
	}
}

// AUTH-02: migration 075 must not fail on a database that already holds
// case-variant duplicates; it renames all but the oldest deterministically.
func TestMigration075RenamesCaseDuplicates(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	sql, err := os.ReadFile("../database/migrations/075_auth_fixes.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	tx, err := f.h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Recreate the pre-075 state inside a transaction that is rolled back.
	if _, err := tx.Exec(ctx, `DROP INDEX users_username_lower_key`); err != nil {
		t.Fatal(err)
	}
	insert := func(name string) int64 {
		var id int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO users (username, password_hash) VALUES ($1,'x') RETURNING id`, name).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
		return id
	}
	a := insert("Mig075-Integration")
	b := insert("mig075-integration")
	c := insert("MIG075-INTEGRATION")
	// Occupy b's first candidate name, forcing the counter suffix.
	insert("mig075-integration-dup" + strconv.FormatInt(b, 10))

	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("migration 075 on duplicates: %v", err)
	}
	name := func(id int64) string {
		var n string
		if err := tx.QueryRow(ctx, `SELECT username FROM users WHERE id=$1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := name(a); got != "Mig075-Integration" {
		t.Errorf("oldest account must keep its name, got %q", got)
	}
	if got, want := name(b), "mig075-integration-dup"+strconv.FormatInt(b, 10)+"-1"; got != want {
		t.Errorf("b renamed to %q, want %q", got, want)
	}
	if got, want := name(c), "MIG075-INTEGRATION-dup"+strconv.FormatInt(c, 10); got != want {
		t.Errorf("c renamed to %q, want %q", got, want)
	}
	var audited int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE username='system' AND entity='user' AND entity_id = ANY($1)`,
		[]int64{b, c}).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 2 {
		t.Errorf("want 2 audit entries for the renames, got %d", audited)
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT s`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO users (username, password_hash) VALUES ('mIg075-integration','x')`); err == nil {
		t.Error("the case-insensitive unique index is missing after the migration")
	}
}

// totpUser creates a user with TOTP enabled and returns its id and secret.
func (f *authFixture) totpUser(t *testing.T, name, password string) (int64, string) {
	t.Helper()
	uid := f.createUser(t, name, password)
	key, err := auth.GenerateTOTP(name)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := f.mgr.EncryptTOTPSecret(key.Secret())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.Pool.Exec(context.Background(),
		`UPDATE users SET totp_enabled=true, totp_secret=$1 WHERE id=$2`, enc, uid); err != nil {
		t.Fatal(err)
	}
	return uid, key.Secret()
}

// AUTH-03: second-factor failures are counted per account in the database and
// escalate into a lock that neither a correct password nor a restart (fresh
// in-memory limiters) lifts. Only a successful second factor resets it.
func TestSecondFactorLockout(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	const name, pw = "totp-lock-Integration", "totp-lock-password"
	uid, secret := f.totpUser(t, name, pw)

	login := func(ah *AuthHandler, code string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		ah.Login(w, authJSONReq(t, http.MethodPost, "/api/auth/login",
			loginRequest{Username: name, Password: pw, TOTPCode: code}))
		return w
	}
	state := func() (int, *time.Time) {
		var n int
		var until *time.Time
		if err := f.h.Pool.QueryRow(ctx,
			`SELECT totp_failures, totp_locked_until FROM users WHERE id=$1`, uid).Scan(&n, &until); err != nil {
			t.Fatal(err)
		}
		return n, until
	}

	// Typos within the free budget cost nothing but the attempt.
	for i := 1; i <= auth.SecondFactorFreeFailures; i++ {
		if w := login(f.ah, "abcdef"); w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: want 401, got %d: %s", i, w.Code, w.Body.String())
		}
	}
	n, until := state()
	if n != auth.SecondFactorFreeFailures || until == nil || time.Until(*until) <= 0 {
		t.Fatalf("after %d failures: failures=%d locked_until=%v, want a lock", n, n, until)
	}

	// A correct password without a code does not reset the counter.
	if w := login(f.ah, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("password-only: want 401 totp_required, got %d", w.Code)
	}
	// A restart (fresh in-memory limiters) does not lift the lock, and even the
	// right code is refused while it lasts.
	restarted := newAuthFixture(t).ah
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w := login(restarted, code)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" ||
		!strings.Contains(w.Body.String(), "totp_required") {
		t.Fatalf("locked: want 429 with Retry-After and totp_required, got %d: %s", w.Code, w.Body.String())
	}
	if n2, _ := state(); n2 != n {
		t.Fatalf("a refused attempt while locked must not count (failures %d -> %d)", n, n2)
	}

	// The next failure after the lock expires escalates: 2 minutes.
	if _, err := f.h.Pool.Exec(ctx,
		`UPDATE users SET totp_locked_until = now() - interval '1 second' WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if w := login(f.ah, "abcdef"); w.Code != http.StatusUnauthorized {
		t.Fatalf("failure after lock: want 401, got %d", w.Code)
	}
	n, until = state()
	if n != auth.SecondFactorFreeFailures+1 || until == nil {
		t.Fatalf("failures=%d locked_until=%v", n, until)
	}
	if d := time.Until(*until); d < 90*time.Second || d > 150*time.Second {
		t.Errorf("second lock should be ~2 min, got %v", d)
	}
	var audited int
	if err := f.h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action='security' AND entity='user' AND entity_id=$1`, uid).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited < 2 {
		t.Errorf("repeated second-factor failures must be audited, got %d entries", audited)
	}

	// Only a successful second factor clears the counter.
	if _, err := f.h.Pool.Exec(ctx,
		`UPDATE users SET totp_locked_until = now() - interval '1 second' WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if w := login(f.ah, code); w.Code != http.StatusOK {
		t.Fatalf("correct code after lock: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if n, until := state(); n != 0 || until != nil {
		t.Errorf("success must reset: failures=%d locked_until=%v", n, until)
	}
}

// AUTH-04: an admin 2FA reset discards a pending enrollment, so an enable that
// arrives after the reset cannot re-arm TOTP with the attacker's secret, and the
// reset lifts a second-factor lock.
func TestResetUserTOTPClearsPendingEnrollment(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	const name = "reset-pending-Integration"
	uid := f.createUser(t, name, "reset-pending-password")
	cookie := f.sessionCookie(t, uid)
	u := &models.User{ID: uid, Username: name}
	withUser := func(r *http.Request) *http.Request {
		r.AddCookie(cookie)
		return r.WithContext(auth.ContextWithUser(ctx, u))
	}

	w := httptest.NewRecorder()
	f.ah.TOTPSetup(w, withUser(httptest.NewRequest(http.MethodPost, "/api/auth/2fa/setup", nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	var setup struct {
		Secret  string `json:"secret"`
		SetupID string `json:"setup_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.Pool.Exec(ctx,
		`UPDATE users SET totp_failures=9, totp_locked_until=now() + interval '1 hour' WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}

	reset := httptest.NewRequest(http.MethodPost, "/api/users/x/reset-2fa", nil)
	reset.SetPathValue("id", strconv.FormatInt(uid, 10))
	w = httptest.NewRecorder()
	f.h.ResetUserTOTP(w, reset)
	if w.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	var pendingSecret, pendingNonce string
	var pendingExp, lockedUntil *time.Time
	var failures int
	if err := f.h.Pool.QueryRow(ctx,
		`SELECT pending_totp_secret, pending_totp_nonce, pending_totp_expires_at, totp_failures, totp_locked_until
		   FROM users WHERE id=$1`, uid).Scan(&pendingSecret, &pendingNonce, &pendingExp, &failures, &lockedUntil); err != nil {
		t.Fatal(err)
	}
	if pendingSecret != "" || pendingNonce != "" || pendingExp != nil {
		t.Error("reset left the pending TOTP enrollment in place")
	}
	if failures != 0 || lockedUntil != nil {
		t.Errorf("reset must lift the second-factor lock: failures=%d locked_until=%v", failures, lockedUntil)
	}

	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	f.ah.TOTPEnable(w, withUser(authJSONReq(t, http.MethodPost, "/api/auth/2fa/enable",
		totpVerifyRequest{Code: code, SetupID: setup.SetupID, Password: "reset-pending-password"})))
	if w.Code != http.StatusConflict {
		t.Fatalf("enable after the reset: want 409 (setup discarded), got %d: %s", w.Code, w.Body.String())
	}
	var enabled bool
	if err := f.h.Pool.QueryRow(ctx, `SELECT totp_enabled FROM users WHERE id=$1`, uid).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Error("totp_enabled is true after the reset")
	}
}

// AUTH-04: login sessions are created only while the verified credential still
// exists — a passkey deleted, or TOTP reset, after verification yields none.
func TestSessionsBoundToVerifiedFactor(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	uid := f.createUser(t, "bound-session-Integration", "bound-session-password")
	credID := []byte("bound-session-cred")
	if _, err := f.h.Pool.Exec(ctx,
		`INSERT INTO webauthn_credentials(user_id,credential_id,public_key,name) VALUES($1,$2,$3,'test')`,
		uid, credID, []byte("key")); err != nil {
		t.Fatal(err)
	}
	req := func() *http.Request { return httptest.NewRequest(http.MethodPost, "/login", nil) }
	count := func() int {
		var n int
		if err := f.h.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, uid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if err := f.mgr.CreatePasskeySession(ctx, httptest.NewRecorder(), req(), uid, credID); err != nil {
		t.Fatalf("passkey session with a live credential: %v", err)
	}
	if _, err := f.h.Pool.Exec(ctx, `DELETE FROM webauthn_credentials WHERE user_id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if err := f.mgr.CreatePasskeySession(ctx, httptest.NewRecorder(), req(), uid, credID); !errors.Is(err, auth.ErrCredentialChanged) {
		t.Fatalf("passkey session after credential removal: want ErrCredentialChanged, got %v", err)
	}
	if n := count(); n != 1 {
		t.Errorf("want exactly the first session, got %d", n)
	}

	var hash string
	if err := f.h.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, uid).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	// TOTP was verified against "enc-secret", then an admin reset cleared it.
	if err := f.mgr.CreatePasswordSession(ctx, httptest.NewRecorder(), req(), uid, hash, "enc-secret", true); !errors.Is(err, auth.ErrCredentialChanged) {
		t.Fatalf("verified session after TOTP reset: want ErrCredentialChanged, got %v", err)
	}
	// A password-only session is unaffected by the TOTP binding.
	if err := f.mgr.CreatePasswordSession(ctx, httptest.NewRecorder(), req(), uid, hash, "", false); err != nil {
		t.Fatalf("password-only session: %v", err)
	}
}

// AUTH-05: a passkey assertion whose userHandle names no account (or is
// malformed) is an ordinary verification failure: 401, and the attempt is NOT
// refunded, so forged finishes spend the login budget like any other.
func TestPasskeyLoginUnknownUserHandle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		handle []byte
	}{
		{"nonexistent id", func() []byte {
			b := make([]byte, 8)
			binary.BigEndian.PutUint64(b, 999_999_999)
			return b
		}()},
		{"malformed handle", []byte{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthFixture(t)
			wa, err := auth.NewWebAuthnService(f.h.Pool, "example.com", "Example", []string{"https://example.com"})
			if err != nil {
				t.Fatal(err)
			}
			f.ah.WebAuthn = wa
			// One attempt per window: a refund would let the next begin through.
			f.ah.Limiter = auth.NewLoginLimiter(1, time.Minute, time.Minute)
			t.Cleanup(func() { _, _ = f.h.Pool.Exec(context.Background(), `DELETE FROM webauthn_ceremonies`) })

			w := httptest.NewRecorder()
			f.ah.PasskeyLoginBegin(w, httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/begin", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("begin: %d %s", w.Code, w.Body.String())
			}
			var opts struct {
				PublicKey struct {
					Challenge string `json:"challenge"`
				} `json:"publicKey"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &opts); err != nil {
				t.Fatal(err)
			}
			var cer *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == waCookie {
					cer = c
				}
			}
			if cer == nil {
				t.Fatal("no ceremony cookie")
			}

			b64 := base64.RawURLEncoding.EncodeToString
			clientData, _ := json.Marshal(map[string]string{
				"type": "webauthn.get", "challenge": opts.PublicKey.Challenge, "origin": "https://example.com",
			})
			rpHash := sha256.Sum256([]byte("example.com"))
			authData := append(rpHash[:], 0x05, 0, 0, 0, 1) // UP|UV, counter 1
			rawID := []byte("forged-credential")
			body, _ := json.Marshal(map[string]any{
				"id": b64(rawID), "rawId": b64(rawID), "type": "public-key",
				"response": map[string]string{
					"clientDataJSON":    b64(clientData),
					"authenticatorData": b64(authData),
					"signature":         b64([]byte("not-a-signature")),
					"userHandle":        b64(tc.handle),
				},
			})
			fin := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/finish", bytes.NewReader(body))
			fin.Header.Set("Content-Type", "application/json")
			fin.AddCookie(cer)
			w = httptest.NewRecorder()
			f.ah.PasskeyLoginFinish(w, fin)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("forged userHandle: want 401, got %d: %s", w.Code, w.Body.String())
			}

			w = httptest.NewRecorder()
			f.ah.PasskeyLoginBegin(w, httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/begin", nil))
			if w.Code != http.StatusTooManyRequests {
				t.Errorf("the forged finish was refunded: next begin got %d, want 429", w.Code)
			}
		})
	}
}
