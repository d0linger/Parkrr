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
