//go:build integration

package auth

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSecondFactorAccountingRejectsRevokedCredentials(t *testing.T) {
	m, pool := testAuthManager(t)
	ctx := t.Context()
	oldSecret, err := m.EncryptTOTPSecret("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	newSecret, err := m.EncryptTOTPSecret("GEZDGNBVGY3TQOJQ")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		enabled bool
		secret  string
		locked  bool
		deleted bool
	}{
		{name: "re-enrolled unlocked", enabled: true, secret: newSecret},
		{name: "re-enrolled locked", enabled: true, secret: newSecret, locked: true},
		{name: "disabled", secret: oldSecret},
		{name: "deleted", deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uid := mkAuthUser(t, pool, "reader", false)
			if _, err := pool.Exec(ctx,
				`UPDATE users SET totp_enabled=TRUE, totp_secret=$2 WHERE id=$1`, uid, oldSecret); err != nil {
				t.Fatal(err)
			}
			// The old login has reserved an attempt before the factor changes.
			if _, _, err := m.ReserveSecondFactorAttempt(ctx, uid, oldSecret); err != nil {
				t.Fatalf("reserve old credential: %v", err)
			}
			if tc.deleted {
				if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := pool.Exec(ctx,
					`UPDATE users SET totp_enabled=$2, totp_secret=$3, totp_failures=7,
					        totp_locked_until=CASE WHEN $4 THEN now() + interval '1 hour' ELSE NULL END,
					        last_totp_step=123456 WHERE id=$1`, uid, tc.enabled, tc.secret, tc.locked); err != nil {
					t.Fatal(err)
				}
			}
			type factorState struct {
				Enabled  bool
				Secret   string
				Failures int
				Locked   *time.Time
				Step     int64
			}
			readState := func() factorState {
				t.Helper()
				var state factorState
				if err := pool.QueryRow(ctx,
					`SELECT totp_enabled, totp_secret, totp_failures, totp_locked_until, last_totp_step
					   FROM users WHERE id=$1`, uid).
					Scan(&state.Enabled, &state.Secret, &state.Failures, &state.Locked, &state.Step); err != nil {
					t.Fatal(err)
				}
				return state
			}
			var before factorState
			if !tc.deleted {
				before = readState()
			}
			operations := []struct {
				name string
				run  func() error
			}{
				{name: "reserve", run: func() error {
					failures, wait, err := m.ReserveSecondFactorAttempt(ctx, uid, oldSecret)
					if failures != 0 || wait != 0 {
						t.Errorf("revoked reservation exposed failure state: count=%d wait=%v", failures, wait)
					}
					return err
				}},
				{name: "release", run: func() error { return m.ReleaseSecondFactorAttempt(ctx, uid, oldSecret) }},
				{name: "reset", run: func() error { return m.ResetSecondFactorFailures(ctx, uid, oldSecret) }},
			}
			for _, op := range operations {
				if err := op.run(); !errors.Is(err, ErrCredentialChanged) {
					t.Errorf("%s revoked credential: got %v, want ErrCredentialChanged", op.name, err)
				}
				if !tc.deleted && !reflect.DeepEqual(before, readState()) {
					t.Fatalf("%s changed the replacement factor's state", op.name)
				}
			}
		})
	}
}

func TestSecondFactorAccountingPreservesCurrentCredentialLockout(t *testing.T) {
	m, pool := testAuthManager(t)
	ctx := t.Context()
	uid := mkAuthUser(t, pool, "reader", false)
	secret, err := m.EncryptTOTPSecret("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET totp_enabled=TRUE, totp_secret=$2 WHERE id=$1`, uid, secret); err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= SecondFactorFreeFailures; want++ {
		failures, wait, err := m.ReserveSecondFactorAttempt(ctx, uid, secret)
		if err != nil || failures != want || wait != 0 {
			t.Fatalf("reserve %d: count=%d wait=%v err=%v", want, failures, wait, err)
		}
	}
	for range 2 {
		failures, wait, err := m.ReserveSecondFactorAttempt(ctx, uid, secret)
		if err != nil || failures != SecondFactorFreeFailures || wait <= 0 || wait > time.Minute {
			t.Fatalf("locked reservation changed its budget: count=%d wait=%v err=%v", failures, wait, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET totp_locked_until=now() - interval '1 second' WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if failures, wait, err := m.ReserveSecondFactorAttempt(ctx, uid, secret); err != nil || failures != SecondFactorFreeFailures+1 || wait != 0 {
		t.Fatalf("reserve after lock expires: count=%d wait=%v err=%v", failures, wait, err)
	}
	if failures, wait, err := m.ReserveSecondFactorAttempt(ctx, uid, secret); err != nil || failures != SecondFactorFreeFailures+1 || wait <= time.Minute || wait > 2*time.Minute {
		t.Fatalf("lock did not escalate: count=%d wait=%v err=%v", failures, wait, err)
	}
	if err := m.ResetSecondFactorFailures(ctx, uid, secret); err != nil {
		t.Fatalf("reset current credential: %v", err)
	}
	if failures, wait, err := m.ReserveSecondFactorAttempt(ctx, uid, secret); err != nil || failures != 1 || wait != 0 {
		t.Fatalf("reserve after success: count=%d wait=%v err=%v", failures, wait, err)
	}
	// Releasing a reservation must not make the counter negative, and resetting
	// an already-clean current credential must still succeed.
	for range 2 {
		if err := m.ReleaseSecondFactorAttempt(ctx, uid, secret); err != nil {
			t.Fatalf("release current credential: %v", err)
		}
	}
	var failures int
	var locked *time.Time
	if err := pool.QueryRow(ctx, `SELECT totp_failures, totp_locked_until FROM users WHERE id=$1`, uid).
		Scan(&failures, &locked); err != nil {
		t.Fatal(err)
	}
	if failures != 0 || locked != nil {
		t.Fatalf("release did not preserve clean state: count=%d lock=%v", failures, locked)
	}
	if err := m.ResetSecondFactorFailures(ctx, uid, secret); err != nil {
		t.Fatalf("reset clean current credential: %v", err)
	}
}
