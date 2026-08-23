package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestUpdateUserMissingReturns404: editing a user id that doesn't exist must 404,
// not report a phantom success (the UPDATE would touch 0 rows yet return 200).
func TestUpdateUserMissingReturns404(t *testing.T) {
	h := testHandler(t)
	body, _ := json.Marshal(userRequest{Username: "ghost", Email: "ghost@example.com", Role: "admin"})
	req := httptest.NewRequest(http.MethodPut, "/api/users/999999", bytes.NewReader(body))
	req.SetPathValue("id", "999999")
	rec := httptest.NewRecorder()
	h.UpdateUser(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a nonexistent user, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestResetUser2FAClearsBackupCodesAndPasskeys(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	var userID int64
	err := h.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role, is_admin, totp_enabled, totp_secret)
		 VALUES ('reset_2fa_user', 'reset2fa@example.com', 'hash', 'editor', false, true, 'secret')
		 RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO totp_backup_codes (user_id, code_hash) VALUES ($1, 'hash1')`, userID); err != nil {
		t.Fatalf("insert totp_backup_code: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO webauthn_credentials (user_id, credential_id, public_key, name)
		 VALUES ($1, $2, $3, 'Test Key')`, userID, []byte("cred123"), []byte("pubkey123")); err != nil {
		t.Fatalf("insert webauthn_credential: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/users/1/reset-2fa", nil)
	req.SetPathValue("id", strconv.FormatInt(userID, 10))
	rec := httptest.NewRecorder()
	h.ResetUserTOTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ResetUserTOTP status %d body %s", rec.Code, rec.Body.String())
	}

	var totpEnabled bool
	if err := h.Pool.QueryRow(ctx, `SELECT totp_enabled FROM users WHERE id=$1`, userID).Scan(&totpEnabled); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if totpEnabled {
		t.Error("expected totp_enabled to be false after 2FA reset")
	}

	var backupCount int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM totp_backup_codes WHERE user_id=$1`, userID).Scan(&backupCount); err != nil {
		t.Fatalf("query backup codes: %v", err)
	}
	if backupCount != 0 {
		t.Errorf("expected 0 backup codes remaining, got %d", backupCount)
	}

	var passkeyCount int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM webauthn_credentials WHERE user_id=$1`, userID).Scan(&passkeyCount); err != nil {
		t.Fatalf("query webauthn credentials: %v", err)
	}
	if passkeyCount != 0 {
		t.Errorf("expected 0 webauthn credentials remaining, got %d", passkeyCount)
	}
}
