package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const backupCodeCount = 10

// codeAlphabet excludes easily-confused characters (0/O, 1/I/L).
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// GenerateBackupCodes creates a fresh set of human-friendly one-time codes
// (formatted like ABCD-EFGH) and stores their hashes, replacing any existing
// codes for the user. The plaintext codes are returned once for display.
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
		batch = append(batch, []any{userID, hashCode(c)})
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
	var id int64
	err := m.pool.QueryRow(ctx,
		`UPDATE totp_backup_codes SET used_at = now()
		 WHERE id = (SELECT id FROM totp_backup_codes
		             WHERE user_id=$1 AND code_hash=$2 AND used_at IS NULL
		             LIMIT 1)
		 RETURNING id`, userID, hashCode(code)).Scan(&id)
	return err == nil
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
	var sb strings.Builder
	b := make([]byte, 1)
	for i := 0; i < 8; {
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		v := b[0]
		// Reject values >= 248 to eliminate modulo bias. With len(codeAlphabet) == 31,
		// 31 * 8 = 248. This ensures each character is picked with uniform probability.
		if v >= 248 {
			continue
		}
		if i == 4 {
			sb.WriteByte('-')
		}
		sb.WriteByte(codeAlphabet[int(v)%len(codeAlphabet)])
		i++
	}
	return sb.String(), nil
}
