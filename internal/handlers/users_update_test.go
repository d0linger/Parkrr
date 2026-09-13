package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
)

// TestUpdateUserMissingReturns404: editing a user id that doesn't exist must 404,
// not report a phantom success (the UPDATE would touch 0 rows yet return 200).
func TestUpdateUserMissingReturns404(t *testing.T) {
	h := testHandler(t)
	body, _ := json.Marshal(userRequest{Username: "ghost", Email: "ghost@example.com", Role: "admin"})
	req := httptest.NewRequest(http.MethodPut, "/api/users/999999", bytes.NewReader(body))
	req.SetPathValue("id", "999999")
	rec := httptest.NewRecorder()
	h.UpdateUser(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a nonexistent user, got %d %s", rec.Code, rec.Body.String())
	}
}

// PR #127: Der administrative 2FA-Reset muss ALLE zweiten Faktoren loeschen —
// TOTP-Backup-Codes UND WebAuthn-Passkeys — sowie aktive Sitzungen beenden.
func TestResetUser2FAClearsBackupCodesAndPasskeys(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	var userID int64
	err := h.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role, is_admin, totp_enabled, totp_secret)
		 VALUES ('reset_2fa_user', 'reset2fa@example.com', 'hash', 'editor', false, true, 'secret')
		 RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO totp_backup_codes (user_id, code_hash) VALUES ($1, 'hash1')`, userID); err != nil {
		t.Fatalf("insert totp_backup_code: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO webauthn_credentials (user_id, credential_id, public_key, name)
		 VALUES ($1, $2, $3, 'Test Key')`, userID, []byte("cred123"), []byte("pubkey123")); err != nil {
		t.Fatalf("insert webauthn_credential: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO sessions (token, user_id, expires_at)
		 VALUES ('session_token_reset2fa', $1, now() + interval '1 day')`, userID); err != nil {
		t.Fatalf("insert session: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/users/1/reset-2fa", nil)
	req.SetPathValue("id", strconv.FormatInt(userID, 10))
	rec := httptest.NewRecorder()
	h.ResetUserTOTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ResetUserTOTP status %d body %s", rec.Code, rec.Body.String())
	}

	var totpEnabled bool
	var totpSecret string
	if err := h.Pool.QueryRow(ctx,
		`SELECT totp_enabled,totp_secret FROM users WHERE id=$1`, userID).Scan(&totpEnabled, &totpSecret); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if totpEnabled || totpSecret != "" {
		t.Error("expected TOTP disabled and its secret cleared after 2FA reset")
	}

	var backupCount int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM totp_backup_codes WHERE user_id=$1`, userID).Scan(&backupCount); err != nil {
		t.Fatalf("query backup codes: %v", err)
	}
	if backupCount != 0 {
		t.Errorf("expected 0 backup codes remaining, got %d", backupCount)
	}

	var passkeyCount int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM webauthn_credentials WHERE user_id=$1`, userID).Scan(&passkeyCount); err != nil {
		t.Fatalf("query webauthn credentials: %v", err)
	}
	if passkeyCount != 0 {
		t.Errorf("expected 0 webauthn credentials remaining, got %d", passkeyCount)
	}

	var sessionCount int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, userID).Scan(&sessionCount); err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	if sessionCount != 0 {
		t.Errorf("expected 0 sessions remaining, got %d", sessionCount)
	}
}

func TestResetUserTOTPRevokesOnlyTargetSessions(t *testing.T) {
	h := testHandler(t)
	ctx := t.Context()
	m, err := auth.NewManager(
		h.Pool,
		auth.SessionConfig{MaxAge: 3600},
		false,
		false,
		"isolated-reset-test-session-secret",
	)
	if err != nil {
		t.Fatal(err)
	}
	createUser := func(username, role string) int64 {
		t.Helper()
		var id int64
		if err := h.Pool.QueryRow(ctx,
			`INSERT INTO users (username,email,password_hash,role,is_admin,totp_enabled,totp_secret)
			 VALUES ($1,$2,'dummy',$3,$4,true,'dummy-secret') RETURNING id`,
			username, username+"@example.com", role, role == "admin").Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := h.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id); err != nil {
				t.Errorf("cleanup user: %v", err)
			}
		})
		return id
	}
	targetID := createUser("reset_sessions_target", "editor")
	adminID := createUser("reset_sessions_admin", "admin")
	issueCookies := func(id int64) []*http.Cookie {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		if err := m.CreateVerifiedSession(ctx, rec, req, id); err != nil {
			t.Fatal(err)
		}
		return rec.Result().Cookies()
	}
	first, second, admin := issueCookies(targetID), issueCookies(targetID), issueCookies(adminID)
	withCookies := func(req *http.Request, cookies []*http.Cookie, csrf bool) *http.Request {
		for _, cookie := range cookies {
			req.AddCookie(cookie)
			if csrf && cookie.Name == auth.CSRFCookie {
				req.Header.Set(auth.CSRFHeader, cookie.Value)
			}
		}
		return req
	}
	protected := m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	probe := func(cookies []*http.Cookie) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
		protected.ServeHTTP(rec, withCookies(req, cookies, false))
		return rec.Code
	}
	reset := m.RequireAdmin(http.HandlerFunc(h.ResetUserTOTP))
	requestReset := func(cookies []*http.Cookie, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/users/target/reset-2fa", nil)
		req.SetPathValue("id", strconv.FormatInt(targetID, 10))
		rec := httptest.NewRecorder()
		reset.ServeHTTP(rec, withCookies(req, cookies, csrf))
		return rec
	}
	for _, tc := range []struct {
		name    string
		cookies []*http.Cookie
		csrf    bool
		want    int
	}{
		{name: "anonymous denied", cookies: []*http.Cookie{}, want: http.StatusUnauthorized},
		{name: "editor denied", cookies: first, csrf: true, want: http.StatusForbidden},
		{name: "missing csrf denied", cookies: admin, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := requestReset(tc.cookies, tc.csrf); rec.Code != tc.want {
				t.Fatalf("reset: %d %s; want %d", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
	for _, cookies := range [][]*http.Cookie{first, second, admin} {
		if status := probe(cookies); status != http.StatusNoContent {
			t.Fatalf("precondition: live cookie rejected with %d", status)
		}
	}
	for _, name := range []string{"first reset", "repeated reset"} {
		t.Run(name, func(t *testing.T) {
			if rec := requestReset(admin, true); rec.Code != http.StatusOK {
				t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
			}
			for _, tc := range []struct {
				name    string
				cookies []*http.Cookie
				want    int
			}{
				{name: "first target cookie revoked", cookies: first, want: http.StatusUnauthorized},
				{name: "second target cookie revoked", cookies: second, want: http.StatusUnauthorized},
				{name: "other user cookie preserved", cookies: admin, want: http.StatusNoContent},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if got := probe(tc.cookies); got != tc.want {
						t.Fatalf("got status %d; want %d", got, tc.want)
					}
				})
			}
		})
	}
}

func TestResetUserTOTPRollsBackWhenSessionRevocationFails(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var id int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO users (username,email,password_hash,role,totp_enabled,totp_secret)
		 VALUES ('reset_rollback','reset-rollback@example.com','dummy','editor',true,'original-secret')
		 RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := h.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, id); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO totp_backup_codes (user_id,code_hash) VALUES ($1,'original-code')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO webauthn_credentials (user_id,credential_id,public_key,name)
		 VALUES ($1,$2,$3,'original-key')`, id, []byte("rollback-credential"), []byte("original-public-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO sessions (token,user_id,expires_at)
		 VALUES ('rollback-session',$1,now()+interval '1 day')`, id); err != nil {
		t.Fatal(err)
	}
	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := blocker.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback blocker: %v", err)
		}
	}()
	var token string
	if err := blocker.QueryRow(ctx,
		`SELECT token FROM sessions WHERE user_id=$1 FOR UPDATE`, id).Scan(&token); err != nil {
		t.Fatal(err)
	}
	// Only session deletion conflicts with this row lock. Use a separate test
	// pool's lock timeout, leaving the request alive so normal rollback can run.
	cfg := h.Pool.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.MinIdleConns = 0
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "200ms"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var lockTimeout string
	if err := pool.QueryRow(ctx, `SHOW lock_timeout`).Scan(&lockTimeout); err != nil {
		t.Fatal(err)
	}
	if lockTimeout != "200ms" {
		t.Fatalf("test pool lock_timeout=%q; want 200ms", lockTimeout)
	}
	resetHandler := New(pool)
	requestReset := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/users/target/reset-2fa", nil).WithContext(ctx)
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		rec := httptest.NewRecorder()
		resetHandler.ResetUserTOTP(rec, req)
		return rec
	}
	if rec := requestReset(); rec.Code != http.StatusInternalServerError {
		t.Fatalf("locked reset: %d %s", rec.Code, rec.Body.String())
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("request expired instead of hitting session lock timeout: %v", err)
	}
	var enabled bool
	var secret, code, keyName, savedToken string
	if err := h.Pool.QueryRow(ctx,
		`SELECT u.totp_enabled,u.totp_secret,b.code_hash,w.name,s.token
		 FROM users u JOIN totp_backup_codes b ON b.user_id=u.id
		 JOIN webauthn_credentials w ON w.user_id=u.id JOIN sessions s ON s.user_id=u.id
		 WHERE u.id=$1`, id).Scan(&enabled, &secret, &code, &keyName, &savedToken); err != nil {
		t.Fatalf("read preserved credentials: %v", err)
	}
	if !enabled || secret != "original-secret" {
		t.Fatalf("TOTP state changed on failure: enabled=%v secret=%q", enabled, secret)
	}
	if code != "original-code" {
		t.Fatalf("backup code changed on failure: %q", code)
	}
	if keyName != "original-key" || savedToken != token {
		t.Fatalf("passkey/session changed on failure: key=%q token=%q", keyName, savedToken)
	}
	var auditCount int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		 WHERE entity='user' AND entity_id=$1 AND summary LIKE 'reset 2FA%'`, id).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("failed reset wrote %d success audit entries", auditCount)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if rec := requestReset(); rec.Code != http.StatusOK {
		t.Fatalf("retry after releasing lock: %d %s", rec.Code, rec.Body.String())
	}
	var sessionCount int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM sessions WHERE user_id=$1`, id).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatalf("retry left %d sessions", sessionCount)
	}
}
