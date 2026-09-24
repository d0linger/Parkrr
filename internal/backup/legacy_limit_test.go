package backup

import (
	"bytes"
	"crypto/rand"
	"io"
	"strings"
	"testing"
)

// countingReader yields endless pseudo-random bytes and records how many were read.
type countingReader struct{ n int64 }

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := rand.Read(p)
	c.n += int64(n)
	return n, err
}

// BAK-07: an upload that is neither v3 nor a recognizable legacy archive used to be
// read in full (up to 1 GiB) and then decrypted into a second buffer of the same
// size. It must now be rejected after the few header bytes.
func TestDecryptStreamRejectsUnknownFormatBeforeBuffering(t *testing.T) {
	src := &countingReader{}
	err := DecryptStream(io.Discard, src, "some-backup-key")
	if err == nil {
		t.Fatal("random input was accepted")
	}
	if src.n > 64<<10 {
		t.Fatalf("read %d bytes of garbage before rejecting it", src.n)
	}
}

func v1Archive(t *testing.T, plain []byte, key string) []byte {
	t.Helper()
	a, err := aeadV1(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return a.Seal(append([]byte(nil), nonce...), nonce, plain, nil)
}

// Legacy v1 archives carry no header; they are recognized by their pg_dump payload.
func TestDecryptStreamStillReadsLegacyV1(t *testing.T) {
	const key = "legacy-v1-backup-passphrase"
	plain := append([]byte("PGDMP"), bytes.Repeat([]byte{0x01, 0xfe}, 5000)...)
	enc := v1Archive(t, plain, key)
	var got bytes.Buffer
	if err := DecryptStream(&got, bytes.NewReader(enc), key); err != nil {
		t.Fatalf("DecryptStream(v1): %v", err)
	}
	if !bytes.Equal(got.Bytes(), plain) {
		t.Fatal("v1 plaintext differs")
	}
	if err := DecryptStream(io.Discard, bytes.NewReader(enc), "wrong-key"); err == nil {
		t.Fatal("v1 archive decrypted with the wrong key")
	}
}

func TestDecryptStreamCapsLegacyInput(t *testing.T) {
	t.Cleanup(func() { legacyLimit.Store(0) })
	SetLegacyArchiveLimit(4 << 10)
	const key = "legacy-cap-backup-passphrase"
	enc, err := Encrypt(bytes.Repeat([]byte("x"), 8<<10), key)
	if err != nil {
		t.Fatal(err)
	}
	err = DecryptStream(io.Discard, bytes.NewReader(enc), key)
	if err == nil || !strings.Contains(err.Error(), "parkrr restore") {
		t.Fatalf("oversized legacy archive: err = %v, want a pointer to the CLI", err)
	}
	// Below the limit the same format still works.
	small, err := Encrypt([]byte("small legacy archive"), key)
	if err != nil {
		t.Fatal(err)
	}
	if err := DecryptStream(io.Discard, bytes.NewReader(small), key); err != nil {
		t.Fatalf("small legacy archive: %v", err)
	}
}
