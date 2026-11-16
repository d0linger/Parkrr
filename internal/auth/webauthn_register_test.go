package auth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// A passkey registration verified while an administrator resets the user's factors
// must not store its credential once the reset has removed the session. The test
// plays ResetUserTOTP by hand so it can hold its transaction open: users row
// locked, then sessions deleted and committed while the insert waits.
func TestSaveCredentialLosesToConcurrentFactorReset(t *testing.T) {
	m, pool := testAuthManager(t)
	userID := mkAuthUser(t, pool, "editor", false)
	service := &WebAuthnService{pool: pool}
	var token string
	for _, c := range sessionCookies(t, m, userID) {
		if c.Name == SessionCookie {
			token = hashToken(c.Value)
		}
	}
	if token == "" {
		t.Fatal("no session cookie")
	}
	cred := func(tag string) *webauthn.Credential {
		return &webauthn.Credential{ID: []byte(fmt.Sprintf("reset-race-%s-%d", tag, time.Now().UnixNano())), PublicKey: []byte{1}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if saved, err := service.saveCredential(ctx, userID, token, cred("live"), "key"); err != nil || !saved {
		t.Fatalf("live session: saved=%v err=%v", saved, err)
	}

	reset, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reset.Rollback(context.Background()) }()
	if _, err := reset.Exec(ctx, `UPDATE users SET totp_enabled=FALSE, updated_at=now() WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		saved bool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		saved, err := service.saveCredential(ctx, userID, token, cred("raced"), "key")
		done <- result{saved, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
			   AND wait_event_type='Lock' AND query LIKE '%FROM users WHERE id = $1 FOR SHARE%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("credential insert did not wait for the reset: saved=%v err=%v", r.saved, r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("credential insert never waited for the user row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := reset.Exec(ctx, `DELETE FROM webauthn_credentials WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := reset.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if err := reset.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.saved {
			t.Fatalf("after the reset: saved=%v err=%v, want not saved", r.saved, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credential insert did not finish after the reset")
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM webauthn_credentials WHERE user_id=$1`, userID).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d passkey(s) survived the factor reset", left)
	}
}

// registerSession creates a real session for a fresh user and returns the user
// id and the stored (hashed) session token.
func registerSession(t *testing.T) (*WebAuthnService, int64, string) {
	t.Helper()
	m, pool := testAuthManager(t)
	userID := mkAuthUser(t, pool, "editor", false)
	for _, c := range sessionCookies(t, m, userID) {
		if c.Name == SessionCookie {
			return &WebAuthnService{pool: pool}, userID, hashToken(c.Value)
		}
	}
	t.Fatal("no session cookie")
	return nil, 0, ""
}

// waitForLockWait blocks until a statement matching like waits on a lock.
func waitForLockWait(t *testing.T, ctx context.Context, s *WebAuthnService, like string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
			   AND wait_event_type='Lock' AND query LIKE $1)`, like).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if done() {
			t.Fatalf("finished without waiting on the lock (%s)", like)
		}
		if time.Now().After(deadline) {
			t.Fatalf("never waited on the lock (%s)", like)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Logout deletes only the session row, without touching the user. A logout that
// is already deleting the session must win: the insert waits and stores nothing.
func TestSaveCredentialLosesToConcurrentLogout(t *testing.T) {
	service, userID, token := registerSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	logout, err := service.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logout.Rollback(context.Background()) }()
	if _, err := logout.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, token); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		saved, err := service.saveCredential(ctx, userID, token,
			&webauthn.Credential{ID: []byte(fmt.Sprintf("logout-race-%d", time.Now().UnixNano())), PublicKey: []byte{1}}, "key")
		done <- saved && err == nil
	}()
	waitForLockWait(t, ctx, service, "%FROM sessions WHERE token = $1%FOR SHARE%", func() bool { return len(done) > 0 })
	if err := logout.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case saved := <-done:
		if saved {
			t.Fatal("passkey stored after logout committed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credential insert did not finish after logout")
	}
}

// A session that expires while the insert waits for the user lock must not count:
// now() would still be the transaction start, before the wait.
func TestSaveCredentialRejectsSessionExpiredDuringLockWait(t *testing.T) {
	service, userID, token := registerSession(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	holder, err := service.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(context.Background()) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx,
		`UPDATE sessions SET expires_at = clock_timestamp() + interval '1 second' WHERE token = $1`, token); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		saved, err := service.saveCredential(ctx, userID, token,
			&webauthn.Credential{ID: []byte(fmt.Sprintf("expiry-race-%d", time.Now().UnixNano())), PublicKey: []byte{1}}, "key")
		done <- saved && err == nil
	}()
	waitForLockWait(t, ctx, service, "%FROM users WHERE id = $1 FOR SHARE%", func() bool { return len(done) > 0 })
	var stillValid bool
	if err := service.pool.QueryRow(ctx, `SELECT expires_at > clock_timestamp() FROM sessions WHERE token = $1`, token).Scan(&stillValid); err != nil || !stillValid {
		t.Fatalf("the session expired before the insert started waiting (valid=%v err=%v)", stillValid, err)
	}
	time.Sleep(1300 * time.Millisecond)
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case saved := <-done:
		if saved {
			t.Fatal("passkey stored for a session that expired during the lock wait")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credential insert did not finish")
	}
}
