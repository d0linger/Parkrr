package backup

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	const key = "a-separate-backup-passphrase-with-entropy"
	plain := []byte("PGDMP\x00\x01\x02 pretend custom-format dump with \xff binary bytes")

	enc, err := Encrypt(plain, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(enc, plain) {
		t.Fatal("ciphertext still contains the plaintext")
	}

	got, err := Decrypt(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("round-trip mismatch")
	}
}

func TestDecryptRejectsWrongKeyAndTampering(t *testing.T) {
	const key = "the-real-backup-passphrase-value"
	enc, err := Encrypt([]byte("secret dump"), key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := Decrypt(enc, "a-different-passphrase-entirely!!"); err == nil {
		t.Error("a wrong key must not decrypt")
	}
	bad := append([]byte(nil), enc...)
	bad[len(bad)-1] ^= 0xff // flip a tag byte
	if _, err := Decrypt(bad, key); err == nil {
		t.Error("a tampered backup must fail authentication")
	}
	if _, err := Decrypt(enc[:8], key); err == nil {
		t.Error("a truncated backup must be rejected")
	}
}

func TestKeyRequired(t *testing.T) {
	if _, err := Encrypt([]byte("x"), ""); err == nil {
		t.Error("an empty backup key must be rejected")
	}
}
