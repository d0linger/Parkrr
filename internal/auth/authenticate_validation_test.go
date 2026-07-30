package auth

import (
	"context"
	"strings"
	"testing"
)

// TestAuthenticateRejectsOversizedInputs verifies the defense-in-depth guard:
// an over-long username or password is rejected before any DB lookup or bcrypt
// compare. The Manager has a nil pool, so the guard must return first — a DB
// access would panic — proving no query/bcrypt work happens on oversized input.
func TestAuthenticateRejectsOversizedInputs(t *testing.T) {
	m := &Manager{} // nil pool on purpose
	ctx := context.Background()

	if _, err := m.Authenticate(ctx, strings.Repeat("a", maxAuthUsernameLen+1), "pw"); err == nil {
		t.Error("over-long username should be rejected before any DB/bcrypt work")
	}
	if _, err := m.Authenticate(ctx, "user", strings.Repeat("x", maxAuthPasswordLen+1)); err == nil {
		t.Error("over-long password should be rejected before any DB/bcrypt work")
	}
}
