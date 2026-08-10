package auth

import (
	"context"
	"crypto/rand"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const backupCodeCount = 10

// codeAlphabet excludes easily-confused characters (0/O, 1/I/L).
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// normalizeCode upper-cases and trims a code so user entry is case- and
// whitespace-insensitive.
func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// hashCode returns a bcrypt hash of the normalized code, at the same cost as
// password hashing.
func hashCode(code string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(normalizeCode(code)), bcrypt.DefaultCost)
	return string(b), err
}

// GenerateBackupCodes creates a fresh set of human-friendly one-time codes
// (formatted like ABCD-EFGH-IJKL) and stores their hashes, replacing any
// existing codes for the user. The plaintext codes are returned once for display.
func (m *Manager) GenerateBackupCodes(ctx context.Context, userID int64) ([]string, error) {
	codes := make([]string, 0, backupCodeCount)
	for i := 0; i < backupCodeCount; i++ {
		c, err := randomCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}

	batch := make([][]any, 0, len(codes))
	for _, c := range codes {
		h, err := hashCode(c)
		if err != nil {
			return nil, err
		}
		batch = append(batch, []any{userID, h})
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM totp_backup_codes WHERE user_id=$1`, userID); err != nil {
		return nil, err
	}
	for _, row := range batch {
		if _, err := tx.Exec(ctx,
			`INSERT INTO totp_backup_codes (user_id, code_hash) VALUES ($1,$2)`,
			row[0], row[1]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return codes, nil
}

// ConsumeBackupCode marks a matching unused code as used and returns true if it
// was valid. Codes are single-use.
func (m *Manager) ConsumeBackupCode(ctx context.Context, userID int64, code string) bool {
	// bcrypt hashes are salted, so the code can't be looked up by hash; compare
	// against each of the user's unused codes instead.
	rows, err := m.pool.Query(ctx,
		`SELECT id, code_hash FROM totp_backup_codes WHERE user_id=$1 AND used_at IS NULL`, userID)
	if err != nil {
		return false
	}
	defer rows.Close()

	norm := normalizeCode(code)
	for rows.Next() {
		var id int64
		var hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return false
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(norm)) != nil {
			continue
		}
		// Claim the code atomically: the used_at IS NULL guard means a concurrent
		// spend of the same code affects zero rows and is rejected.
		ct, err := m.pool.Exec(ctx,
			`UPDATE totp_backup_codes SET used_at = now() WHERE id = $1 AND used_at IS NULL`, id)
		return err == nil && ct.RowsAffected() == 1
	}
	return false
}

// DeleteBackupCodes removes all backup codes for a user (on 2FA disable).
func (m *Manager) DeleteBackupCodes(ctx context.Context, userID int64) {
	_, _ = m.pool.Exec(ctx, `DELETE FROM totp_backup_codes WHERE user_id=$1`, userID)
}

// RemainingBackupCodes counts a user's unused backup codes.
func (m *Manager) RemainingBackupCodes(ctx context.Context, userID int64) (int, error) {
	var n int
	err := m.pool.QueryRow(ctx,
		`SELECT count(*) FROM totp_backup_codes WHERE user_id=$1 AND used_at IS NULL`,
		userID).Scan(&n)
	return n, err
}

func randomCode() (string, error) {
	const n = 12
	// Reject bytes at or above the largest multiple of the alphabet length so the
	// modulo maps uniformly (no bias toward the low residues).
	limit := 256 - (256 % len(codeAlphabet))
	chars := make([]byte, 0, n)
	buf := make([]byte, 1)
	for len(chars) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) >= limit {
			continue // rejection-sample for a uniform distribution
		}
		chars = append(chars, codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	return string(chars[:4]) + "-" + string(chars[4:8]) + "-" + string(chars[8:]), nil
}
