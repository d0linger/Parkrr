package auth

import (
	"strings"
	"testing"
)

func testManager(t *testing.T, secret string) *Manager {
	t.Helper()
	aead, err := newAEAD(secret)
	if err != nil {
		t.Fatalf("newAEAD: %v", err)
	}
	return &Manager{aead: aead}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	m := testManager(t, "a-sufficiently-long-secret-value")
	const secret = "JBSWY3DPEHPK3PXP"

	enc, err := m.encrypt(secret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == secret || strings.Contains(enc, secret) {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := m.decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != secret {
		t.Fatalf("round-trip mismatch: got %q want %q", got, secret)
	}
}

func TestEncryptNonDeterministic(t *testing.T) {
	m := testManager(t, "secret-secret-secret")
	a, _ := m.encrypt("same")
	b, _ := m.encrypt("same")
	if a == b {
		t.Fatal("expected distinct ciphertexts (random nonce)")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	enc, _ := testManager(t, "key-one-key-one-key").encrypt("topsecret")
	if _, err := testManager(t, "key-two-key-two-key").decrypt(enc); err == nil {
		t.Fatal("decrypt with wrong key must fail (GCM auth)")
	}
}

func TestDecryptTamperFails(t *testing.T) {
	m := testManager(t, "tamper-key-tamper-key")
	enc, _ := m.encrypt("integrity")
	// Flip a character in the base64 ciphertext.
	b := []byte(enc)
	b[len(b)-1] ^= 0x01
	if _, err := m.decrypt(string(b)); err == nil {
		t.Fatal("tampered ciphertext must fail authentication")
	}
}

func TestDecryptTooShort(t *testing.T) {
	m := testManager(t, "short-key-short-key")
	if _, err := m.decrypt("AAAA"); err == nil {
		t.Fatal("too-short ciphertext must error")
	}
}
