// Package backup creates and restores AES-256-GCM-encrypted PostgreSQL dumps.
// The encryption key is derived from a dedicated passphrase (PARKRR_BACKUP_KEY),
// separate from the session secret, so rotating one never invalidates the other.
package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// keyContext domain-separates the backup key from any other SHA-256-derived key.
const keyContext = "parkrr-backup-v1:"

func aead(key string) (cipher.AEAD, error) {
	if key == "" {
		return nil, errors.New("backup key is not set")
	}
	sum := sha256.Sum256([]byte(keyContext + key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Dump produces a PostgreSQL custom-format dump (pg_restore-compatible) of the
// database at dbURL. Requires pg_dump on PATH (matching the server's major).
func Dump(ctx context.Context, dbURL string) ([]byte, error) {
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--no-owner", "--no-privileges", "--dbname="+dbURL)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pg_dump failed: %w: %s", err, errb.String())
	}
	return out.Bytes(), nil
}

// Encrypt seals a dump with AES-256-GCM, returning nonce||ciphertext||tag.
func Encrypt(plain []byte, key string) ([]byte, error) {
	a, err := aead(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plain, nil), nil
}

// Decrypt reverses Encrypt; fails on a wrong key or any tampering (GCM auth).
func Decrypt(enc []byte, key string) ([]byte, error) {
	a, err := aead(key)
	if err != nil {
		return nil, err
	}
	if len(enc) < a.NonceSize() {
		return nil, errors.New("backup file is too short or corrupt")
	}
	nonce, ct := enc[:a.NonceSize()], enc[a.NonceSize():]
	plain, err := a.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed (wrong key or corrupt file): %w", err)
	}
	return plain, nil
}

// Restore decrypts a backup and restores it into the database at dbURL. This is
// DESTRUCTIVE: --clean --if-exists drops and recreates objects. The archive is
// validated (pg_restore --list) before the DB is touched.
func Restore(ctx context.Context, dbURL string, enc []byte, key string) error {
	plain, err := Decrypt(enc, key)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "parkrr-restore-*.dump")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(plain); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Validate the archive header before touching the database.
	if err := exec.CommandContext(ctx, "pg_restore", "--list", tmp.Name()).Run(); err != nil {
		return fmt.Errorf("not a valid pg_dump archive: %w", err)
	}
	var errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "pg_restore",
		"--clean", "--if-exists", "--no-owner", "--no-privileges", "--exit-on-error",
		"--dbname="+dbURL, tmp.Name())
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore failed: %w: %s", err, errb.String())
	}
	return nil
}
