package backup

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func TestStreamArchiveRoundTripAndIntegrity(t *testing.T) {
	plain := bytes.Repeat([]byte("Parkrr-stream-test-"), 70000) // crosses a 1 MiB frame boundary
	key := "correct horse battery staple for backups"
	var encrypted bytes.Buffer
	if err := EncryptStream(&encrypted, bytes.NewReader(plain), key); err != nil {
		t.Fatalf("EncryptStream: %v", err)
	}
	if !bytes.HasPrefix(encrypted.Bytes(), []byte(backupStreamMagic)) {
		t.Fatalf("stream archive does not start with %q", backupStreamMagic)
	}
	var decrypted bytes.Buffer
	if err := DecryptStream(&decrypted, bytes.NewReader(encrypted.Bytes()), key); err != nil {
		t.Fatalf("DecryptStream: %v", err)
	}
	if !bytes.Equal(decrypted.Bytes(), plain) {
		t.Fatal("round-trip plaintext differs")
	}
	viaBytes, err := Decrypt(encrypted.Bytes(), key)
	if err != nil {
		t.Fatalf("Decrypt(v3): %v", err)
	}
	if !bytes.Equal(viaBytes, plain) {
		t.Fatal("byte compatibility API returned different plaintext")
	}

	truncated := append([]byte(nil), encrypted.Bytes()[:encrypted.Len()-8]...)
	if err := DecryptStream(&bytes.Buffer{}, bytes.NewReader(truncated), key); err == nil {
		t.Fatal("truncated stream was accepted")
	}
	tampered := append([]byte(nil), encrypted.Bytes()...)
	tampered[len(tampered)/2] ^= 0x40
	if err := DecryptStream(&bytes.Buffer{}, bytes.NewReader(tampered), key); err == nil {
		t.Fatal("tampered stream was accepted")
	}
	if err := DecryptStream(&bytes.Buffer{}, bytes.NewReader(encrypted.Bytes()), "wrong backup key"); err == nil {
		t.Fatal("wrong key was accepted")
	}
}

func TestDecryptStreamReadsLegacyV2(t *testing.T) {
	plain := []byte("legacy archive remains restorable")
	key := "legacy-compatible-backup-passphrase"
	legacy, err := Encrypt(plain, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	var got bytes.Buffer
	if err := DecryptStream(&got, bytes.NewReader(legacy), key); err != nil {
		t.Fatalf("DecryptStream(legacy): %v", err)
	}
	if !bytes.Equal(got.Bytes(), plain) {
		t.Fatalf("got %q, want %q", got.Bytes(), plain)
	}
}

func TestEncryptStreamRejectsShortWriter(t *testing.T) {
	err := EncryptStream(zeroWriter{}, bytes.NewReader([]byte("payload")), "short-writer-backup-key")
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("EncryptStream error = %v, want io.ErrShortWrite", err)
	}
}

func TestStreamKeysRemainRequestLocal(t *testing.T) {
	t.Parallel()
	type archive struct {
		key  string
		data []byte
		enc  []byte
	}
	archives := []archive{
		{key: "first-independent-stream-key", data: []byte("first payload")},
		{key: "second-independent-stream-key", data: []byte("second payload")},
	}
	for i := range archives {
		var enc bytes.Buffer
		if err := EncryptStream(&enc, bytes.NewReader(archives[i].data), archives[i].key); err != nil {
			t.Fatal(err)
		}
		archives[i].enc = enc.Bytes()
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(archives))
	for i := range archives {
		a := archives[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got bytes.Buffer
			if err := DecryptStream(&got, bytes.NewReader(a.enc), a.key); err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(got.Bytes(), a.data) {
				errCh <- bytes.ErrTooLarge
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent decrypt: %v", err)
	}
}
