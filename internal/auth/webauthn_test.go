package auth

import (
	"errors"
	"testing"
)

// SetSuspendOnClone must be nil-safe (passkeys disabled => service is nil) and
// otherwise set the flag (finding P-06). The full clone-detection + delete path
// runs inside FinishLogin and requires a real cloned-authenticator assertion, so
// it is exercised end-to-end rather than here.
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
