package auth

import "testing"

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
