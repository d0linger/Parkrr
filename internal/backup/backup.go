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

	"golang.org/x/crypto/argon2"
)

// ArchiveInfo is the header metadata of a validated backup (no DB access needed).
type ArchiveInfo struct {
	Created string `json:"created"` // "... created at ..." from the archive TOC
	Entries int    `json:"entries"` // number of TOC entries
}

// Validate decrypts a backup and confirms it is a readable pg_dump custom-format
// archive, returning header metadata — WITHOUT touching any database.
func Validate(ctx context.Context, enc []byte, key string) (ArchiveInfo, error) {
	toc, err := archiveTOC(ctx, enc, key)
	if err != nil {
		return ArchiveInfo{}, err
	}
	return parseTOC(toc), nil
}

// archiveTOC entschlüsselt ein Archiv und gibt sein Inhaltsverzeichnis aus
// (`pg_restore --list`), ohne eine Datenbank anzufassen. Ausgelagert, damit die
// Wiederherstellungsprüfung Kopf- UND Inhaltsstufe aus einem einzigen Lauf ableiten
// kann — sonst liefe pg_restore zweimal über dasselbe Archiv.
func archiveTOC(ctx context.Context, enc []byte, key string) (string, error) {
	plain, err := Decrypt(enc, key)
	if err != nil {
		return "", err
	}
	return plainArchiveTOC(ctx, plain)
}

func plainArchiveTOC(ctx context.Context, plain []byte) (string, error) {
	// No filename means stdin.
	// #nosec G204 -- fixed executable and arguments.
	cmd := exec.CommandContext(ctx, "pg_restore", "--list")
	cmd.Stdin = bytes.NewReader(plain)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("not a valid pg_dump archive: %w: %s",
				err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("not a valid pg_dump archive: %w", err)
	}
	return string(out), nil
}

// parseTOC liest die Kopfdaten aus einem Inhaltsverzeichnis.
func parseTOC(toc string) ArchiveInfo {
	var info ArchiveInfo
	for _, line := range strings.Split(toc, "\n") {
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
	return info
}

// keyContext domain-separates legacy backup keys. New archives use a salted,
// memory-hard Argon2id derivation and carry an authenticated version header.
const keyContext = "parkrr-backup-v1:"

const backupMagic = "PKRRBK02"

const backupSaltSize = 16

func aeadV1(key string) (cipher.AEAD, error) {
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

func aeadV2(key string, salt []byte) (cipher.AEAD, error) {
	if key == "" {
		return nil, errors.New("backup key is not set")
	}
	if len(salt) != backupSaltSize {
		return nil, errors.New("invalid backup salt")
	}
	derived := argon2.IDKey([]byte(key), salt, 3, 64*1024, 2, 32)
	block, err := aes.NewCipher(derived)
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

// Encrypt seals a dump with AES-256-GCM, returning nonce||ciphertext||tag.
func Encrypt(plain []byte, key string) ([]byte, error) {
	salt := make([]byte, backupSaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	a, err := aeadV2(key, salt)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	header := append([]byte(backupMagic), salt...)
	out := append(append([]byte(nil), header...), nonce...)
	return a.Seal(out, nonce, plain, header), nil
}

// Decrypt reverses Encrypt; fails on a wrong key or any tampering (GCM auth).
func Decrypt(enc []byte, key string) ([]byte, error) {
	// V3 is chunk-authenticated so production can stream it, but the byte API is
	// retained for compatibility with callers/tests that already hold an archive.
	if bytes.HasPrefix(enc, []byte(backupStreamMagic)) {
		var plain bytes.Buffer
		if err := decryptV3(&plain, bytes.NewReader(enc), key); err != nil {
			return nil, err
		}
		return plain.Bytes(), nil
	}
	if bytes.HasPrefix(enc, []byte(backupMagic)) {
		if len(enc) < len(backupMagic)+backupSaltSize {
			return nil, errors.New("backup file is too short or corrupt")
		}
		headerLen := len(backupMagic) + backupSaltSize
		header := enc[:headerLen]
		a, err := aeadV2(key, enc[len(backupMagic):headerLen])
		if err != nil {
			return nil, err
		}
		if len(enc) < headerLen+a.NonceSize()+a.Overhead() {
			return nil, errors.New("backup file is too short or corrupt")
		}
		nonce := enc[headerLen : headerLen+a.NonceSize()]
		ct := enc[headerLen+a.NonceSize():]
		plain, err := a.Open(nil, nonce, ct, header)
		if err != nil {
			return nil, fmt.Errorf("decrypt failed (wrong key or corrupt file): %w", err)
		}
		return plain, nil
	}

	// Legacy v1 archives remain restorable so key hardening does not strand an
	// operator's existing disaster-recovery history.
	a, err := aeadV1(key)
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
	// A scheduled dump must never observe a partially restored schema.
	if err := acquireRun(ctx); err != nil {
		return err
	}
	defer releaseRun()
	plain, err := Decrypt(enc, key)
	if err != nil {
		return err
	}
	if _, err := plainArchiveTOC(ctx, plain); err != nil {
		return err
	}
	var errb bytes.Buffer
	dsn, env := dbExecEnv(dbURL)
	// Keep restoration atomic and the database password out of argv.
	// #nosec G204 -- fixed executable; DSN is operator configuration.
	cmd := exec.CommandContext(ctx, "pg_restore",
		"--single-transaction", "--clean", "--if-exists",
		"--no-owner", "--no-privileges", "--dbname="+dsn)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(plain)
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore failed: %w: %s", err, errb.String())
	}
	return nil
}
