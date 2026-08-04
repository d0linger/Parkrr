package handlers

import (
	"context"
	"testing"
)

// TestTOTPReplayGuardSingleUse proves the atomic single-use guarantee the login
// flow relies on: advancing last_totp_step succeeds exactly once per step, so a
// replayed code (same or older step) affects no row and is rejected, while a
// newer step is accepted again. Runs against the real migrated schema (030).
func TestTOTPReplayGuardSingleUse(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	var uid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ('totp-replay-Integration', 'x') RETURNING id`).
		Scan(&uid); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, uid) })

	guard := func(step int64) int64 {
		ct, err := h.Pool.Exec(ctx,
			`UPDATE users SET last_totp_step=$1 WHERE id=$2 AND last_totp_step < $1`, step, uid)
		if err != nil {
			t.Fatalf("guard update: %v", err)
		}
		return ct.RowsAffected()
	}

	const step = int64(56789)
	if n := guard(step); n != 1 {
		t.Fatalf("first use of a step must be accepted (1 row), got %d", n)
	}
	if n := guard(step); n != 0 {
		t.Errorf("replay of the same step must be rejected (0 rows), got %d", n)
	}
	if n := guard(step - 1); n != 0 {
		t.Errorf("an older step must be rejected (0 rows), got %d", n)
	}
	if n := guard(step + 1); n != 1 {
		t.Errorf("a newer step must be accepted (1 row), got %d", n)
	}
}
