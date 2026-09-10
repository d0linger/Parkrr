package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

// SetSuspendOnClone must be nil-safe (passkeys disabled => service is nil) and
// otherwise set the flag. Credential policy is tested separately after the
// cryptographic verification boundary in TestFinishVerifiedLoginClonePolicy.
func TestSetSuspendOnClone(t *testing.T) {
	var nilSvc *WebAuthnService
	nilSvc.SetSuspendOnClone(true) // must not panic on a nil service

	s := &WebAuthnService{}
	if s.suspendOnClone {
		t.Fatal("default should be off")
	}
	s.SetSuspendOnClone(true)
	if !s.suspendOnClone {
		t.Error("SetSuspendOnClone(true) did not set the flag")
	}
}

func TestFinishVerifiedLoginClonePolicy(t *testing.T) {
	for _, suspend := range []bool{false, true} {
		name := "warn"
		if suspend {
			name = "suspend"
		}
		t.Run(name, func(t *testing.T) {
			_, pool := testAuthManager(t)
			id := mkAuthUser(t, pool, "editor", false)
			credentialID := []byte(t.Name())
			if _, err := pool.Exec(t.Context(),
				`INSERT INTO webauthn_credentials(user_id, credential_id, public_key, name) VALUES($1,$2,$3,'test')`,
				id, credentialID, []byte("key")); err != nil {
				t.Fatal(err)
			}
			svc := &WebAuthnService{pool: pool, suspendOnClone: suspend}
			cred := &webauthn.Credential{ID: credentialID}
			cred.Authenticator.CloneWarning = true
			uid, warning, err := svc.finishVerifiedLogin(context.Background(), id, cred)
			if uid != id || !warning {
				t.Fatalf("lost actor or warning: %d %v", uid, warning)
			}
			if suspend && !errors.Is(err, ErrPasskeySuspended) {
				t.Fatalf("suspension must reject login: %v", err)
			}
			if !suspend && err != nil {
				t.Fatal(err)
			}
			var exists bool
			if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM webauthn_credentials WHERE credential_id=$1)`,
				credentialID).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists == suspend {
				t.Fatalf("credential retained=%v, suspension=%v", exists, suspend)
			}
		})
	}
}

func TestWebAuthnInternalErrClassification(t *testing.T) {
	base := errors.New("db down")
	wrapped := internalErr(base)
	if !errors.Is(wrapped, ErrWebAuthnInternal) {
		t.Error("internalErr should be classified as ErrWebAuthnInternal")
	}
	if !errors.Is(wrapped, base) {
		t.Error("internalErr should preserve the original error for logging")
	}
	if errors.Is(errors.New("verification failed"), ErrWebAuthnInternal) {
		t.Error("a plain (verification) error must not be classified as internal")
	}
	if internalErr(nil) != nil {
		t.Error("internalErr(nil) should be nil")
	}
}

func TestUserHandleRoundTrip(t *testing.T) {
	for _, id := range []int64{1, 42, 1 << 20, 9223372036854775807} {
		if got := handleToID(userHandle(id)); got != id {
			t.Errorf("round-trip %d: got %d", id, got)
		}
	}
}

func TestHandleToIDBadLength(t *testing.T) {
	if got := handleToID([]byte{1, 2, 3}); got != 0 {
		t.Errorf("malformed handle should yield 0, got %d", got)
	}
}

func TestNewWebAuthnServiceDisabledWhenNoRPID(t *testing.T) {
	s, err := NewWebAuthnService(nil, "", "Parkrr", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Enabled() {
		t.Fatal("service should be disabled when RPID is empty")
	}
}

func TestNewWebAuthnServiceEnabled(t *testing.T) {
	s, err := NewWebAuthnService(nil, "example.com", "Parkrr", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.Enabled() {
		t.Fatal("service should be enabled with a valid RPID")
	}
}
