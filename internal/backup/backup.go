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
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ArchiveInfo is the header metadata of a validated backup (no DB access needed).
type ArchiveInfo struct {
	Created string `json:"created"` // "... created at ..." from the archive TOC
	Entries int    `json:"entries"` // number of TOC entries
}

// Validate decrypts a backup and confirms it is a readable pg_dump custom-format
// archive, returning header metadata — WITHOUT touching any database.
func Validate(ctx context.Context, enc []byte, key string) (ArchiveInfo, error) {
	var info ArchiveInfo
	plain, err := Decrypt(enc, key)
	if err != nil {
		return info, err
	}
	tmp, err := os.CreateTemp("", "parkrr-validate-*.dump")
	if err != nil {
		return info, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(plain); err != nil {
		_ = tmp.Close()
		return info, err
	}
	if err := tmp.Close(); err != nil {
		return info, err
	}
	// #nosec G204 -- fixed command "pg_restore"; the only argument is a temp file
	// path we created, not user input.
	out, err := exec.CommandContext(ctx, "pg_restore", "--list", tmp.Name()).Output()
	if err != nil {
		return info, fmt.Errorf("not a valid pg_dump archive: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, ";") {
			if i := strings.Index(line, "created at "); i >= 0 {
				info.Created = strings.TrimSpace(line[i+len("created at "):])
			}
			continue
		}
		if strings.TrimSpace(line) != "" {
			info.Entries++
		}
	}
	return info, nil
}

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

// dbExecEnv splits a Postgres connection string into a password-free DSN and the
// environment for the libpq tool, moving any password out of the command line
// (argv is readable via /proc/<pid>/cmdline) into PGPASSWORD. It handles both
// forms libpq/pgx accept: URLs (password in the userinfo and/or a `password`
// query parameter) and keyword/value DSNs (`host=... password=...`). When no
// password is found it returns the original DSN and a nil env (the child then
// inherits the parent environment).
func dbExecEnv(dbURL string) (dsn string, env []string) {
	withPassword := func(clean, pw string) (string, []string) {
		if pw == "" {
			return clean, nil
		}
		return clean, append(os.Environ(), "PGPASSWORD="+pw)
	}

	if strings.Contains(dbURL, "://") { // URL form
		u, err := url.Parse(dbURL)
		if err != nil {
			return dbURL, nil
		}
		pw := ""
		if u.User != nil {
			if p, ok := u.User.Password(); ok {
				pw = p
				u.User = url.User(u.User.Username()) // keep the user, drop the password
			}
		}
		// libpq also accepts the password as a query parameter.
		if q := u.Query(); q.Get("password") != "" {
			if pw == "" {
				pw = q.Get("password")
			}
			q.Del("password")
			u.RawQuery = q.Encode()
		}
		return withPassword(u.String(), pw)
	}

	// Keyword/value DSN form: redact a `password=...` token (unquoted or
	// single-quoted) into PGPASSWORD.
	if m := kvPasswordRe.FindStringSubmatch(dbURL); m != nil {
		clean := strings.TrimSpace(kvPasswordRe.ReplaceAllString(dbURL, "$1"))
		return withPassword(clean, unquoteLibpq(m[2]))
	}
	return dbURL, nil
}

// kvPasswordRe matches a libpq keyword/value `password=` token, capturing the
// leading boundary ($1) and the value ($2, unquoted or single-quoted).
var kvPasswordRe = regexp.MustCompile(`(?i)(^|\s)password\s*=\s*('(?:[^'\\]|\\.)*'|[^\s]+)`)

// unquoteLibpq removes libpq single-quoting (\' and \\ escapes) from a value.
func unquoteLibpq(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		inner := s[1 : len(s)-1]
		inner = strings.ReplaceAll(inner, `\'`, `'`)
		inner = strings.ReplaceAll(inner, `\\`, `\`)
		return inner
	}
	return s
}

// Dump produces a PostgreSQL custom-format dump (pg_restore-compatible) of the
// database at dbURL. Requires pg_dump on PATH (matching the server's major).
func Dump(ctx context.Context, dbURL string) ([]byte, error) {
	var out, errb bytes.Buffer
	dsn, env := dbExecEnv(dbURL)
	// #nosec G204 -- fixed command "pg_dump"; dbURL comes from operator config
	// (PARKRR_DATABASE_URL/PARKRR_DB_*), never from a request. The password is
	// passed via PGPASSWORD, not on the command line.
	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--no-owner", "--no-privileges", "--dbname="+dsn)
	cmd.Env = env // nil => inherit the parent environment
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
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Validate the archive header before touching the database.
	// #nosec G204 -- fixed command "pg_restore"; the argument is a temp file we created.
	if err := exec.CommandContext(ctx, "pg_restore", "--list", tmp.Name()).Run(); err != nil {
		return fmt.Errorf("not a valid pg_dump archive: %w", err)
	}
	var errb bytes.Buffer
	dsn, env := dbExecEnv(dbURL)
	// --single-transaction makes the whole restore atomic: any error rolls back
	// entirely, so a failed restore never leaves the DB half-wiped.
	// #nosec G204 -- fixed command "pg_restore"; dbURL is operator config and the
	// final argument is a temp file we created, neither is request input. The
	// password is passed via PGPASSWORD, not on the command line.
	cmd := exec.CommandContext(ctx, "pg_restore",
		"--single-transaction", "--clean", "--if-exists", "--no-owner", "--no-privileges",
		"--dbname="+dsn, tmp.Name())
	cmd.Env = env
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore failed: %w: %s", err, errb.String())
	}
	return nil
}
