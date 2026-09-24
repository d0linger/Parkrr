package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// TOTPSetup generates a fresh (not yet enabled) TOTP secret for the current
// user and returns the secret plus a QR-code data URI to scan.
func (h *AuthHandler) TOTPSetup(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	if u.TOTPEnabled {
		writeError(w, http.StatusConflict, "two-factor is already enabled")
		return
	}
	if ok, wait := h.CeremonyLimiter.Consume(strings.ToLower(u.Username)); !ok {
		w.Header().Set("Retry-After", formatSeconds(wait))
		writeError(w, http.StatusTooManyRequests, "Zu viele Versuche – bitte in "+formatMinutes(wait)+" erneut versuchen")
		return
	}
	key, err := auth.GenerateTOTP(u.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate secret")
		return
	}
	// Store the pending secret encrypted (enabled only after verification).
	encSecret, err := h.Auth.EncryptTOTPSecret(key.Secret())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not encrypt secret")
		return
	}
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start setup")
		return
	}
	setupID := base64.RawURLEncoding.EncodeToString(nonceBytes)
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE users
		    SET pending_totp_secret=$1, pending_totp_nonce=$2,
		        pending_totp_expires_at=now() + interval '10 minutes', updated_at=now()
		  WHERE id=$3 AND NOT totp_enabled`, encSecret, setupID, u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not store secret")
		return
	}
	if ct.RowsAffected() != 1 {
		writeError(w, http.StatusConflict, "two-factor is already enabled")
		return
	}
	// A pending TOTP secret is security-relevant state, so the attempt is recorded —
	// but only THAT it happened, never the secret. The guard above means this cannot
	// disable an ACTIVE 2FA; enabling is a separate, audited step.
	//
	// The field names deliberately avoid the substring "totp": isSecretField matches it,
	// so `totp_enabled` and friends would be rewritten to ***REDACTED*** — turning two
	// harmless booleans into a payload that carries no information AND falsely signals
	// that a credential was handed to the trail.
	h.auditChange(r, "update", "user", u.ID, u.Username+" started two-factor setup",
		auditSnapshot(map[string]any{"two_factor_setup_started": true, "two_factor_active": false}))
	qr, err := auth.QRCodeDataURI(key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not render QR code")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"secret":      key.Secret(),
		"qr":          qr,
		"otpauth_url": key.URL(),
		"setup_id":    setupID,
	})
}

type totpVerifyRequest struct {
	Code    string `json:"code"`
	SetupID string `json:"setup_id"`
	// Password re-authenticates when the login is no longer recent (step-up,
	// finding SH-02). Ignored while the recent-auth window is still open.
	Password string `json:"password"`
}

// TOTPEnable verifies a code against the pending secret and enables 2FA.
func (h *AuthHandler) TOTPEnable(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	var req totpVerifyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validTOTPCodeLength(trim(req.Code)) {
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	if len(req.SetupID) != 32 {
		writeError(w, http.StatusBadRequest, "start setup first")
		return
	}
	// Step-up: enabling a second factor requires a recent primary-factor login,
	// or the account password if that window has closed (finding SH-02).
	if !h.requireStepUp(w, r, u, req.Password) {
		return
	}
	// Throttle: a 6-digit code is otherwise brute-forceable during enrolment.
	key, ip, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}
	// Recovery codes and totp_enabled belong to ONE transaction. Two separate
	// ones let a second, concurrent enablement DELETE the codes the first had
	// already displayed — GenerateBackupCodes replaces the whole set — so a user
	// could end up holding recovery codes that no longer exist. A double-tap on
	// "enable" is enough: the same 6-digit code validates twice inside its window.
	// The row lock serializes those attempts; the codes are returned only after
	// the commit, so nothing is shown that is not durably stored.
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not enable two-factor")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var encSecret, setupID string
	var expiresAt *time.Time
	var enabled bool
	if err := tx.QueryRow(r.Context(),
		`SELECT pending_totp_secret, pending_totp_nonce, pending_totp_expires_at, totp_enabled
		   FROM users WHERE id=$1 FOR UPDATE`, u.ID).
		Scan(&encSecret, &setupID, &expiresAt, &enabled); err != nil {
		h.refundReauth(key, ip)
		writeError(w, http.StatusInternalServerError, "could not enable two-factor")
		return
	}
	if enabled || encSecret == "" || expiresAt == nil || time.Now().After(*expiresAt) ||
		subtle.ConstantTimeCompare([]byte(setupID), []byte(req.SetupID)) != 1 {
		h.refundReauth(key, ip)
		writeError(w, http.StatusConflict, "two-factor setup expired or was replaced")
		return
	}
	if !h.Auth.ValidateEncryptedTOTP(encSecret, trim(req.Code)) {
		h.recordReauthFailure(key, ip)
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	codes, err := h.Auth.GenerateBackupCodesTx(r.Context(), tx, u.ID)
	if err != nil {
		h.refundReauth(key, ip)
		writeError(w, http.StatusInternalServerError, "could not generate backup codes")
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE users
		    SET totp_secret=pending_totp_secret, totp_enabled=TRUE,
		        pending_totp_secret='', pending_totp_nonce='', pending_totp_expires_at=NULL,
		        totp_failures=0, totp_locked_until=NULL,
		        updated_at=now()
		  WHERE id=$1`, u.ID); err != nil {
		h.refundReauth(key, ip)
		writeError(w, http.StatusInternalServerError, "could not enable two-factor")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		h.refundReauth(key, ip)
		writeError(w, http.StatusInternalServerError, "could not enable two-factor")
		return
	}
	// Reset the throttle only after the enable has FULLY succeeded (code valid,
	// row updated, backup codes issued), so a failure at any step keeps the
	// accumulated attempts counted.
	h.resetReauth(key, ip)
	h.auditChange(r, "update", "user", u.ID, "enabled two-factor authentication",
		diffFields(map[string]any{"totp_enabled": false}, map[string]any{"totp_enabled": true}))
	writeJSON(w, http.StatusOK, map[string]any{"status": "enabled", "backup_codes": codes})
}

type totpDisableRequest struct {
	Password string `json:"password"`
}

// TOTPDisable turns off 2FA after re-authenticating with the password.
func (h *AuthHandler) TOTPDisable(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	var req totpDisableRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// An over-long password can't match (bcrypt caps at 72 bytes), so
	// reject it up front rather than spending a bcrypt compare on it.
	if len(req.Password) > maxPasswordLen {
		writeError(w, http.StatusForbidden, "Passwort ist falsch")
		return
	}

	key, ip, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}

	if _, err := h.Auth.AuthenticateUserID(r.Context(), u.ID, req.Password); err != nil {
		h.recordReauthFailure(key, ip)
		writeError(w, http.StatusForbidden, "Passwort ist falsch")
		return
	}
	h.resetReauth(key, ip)

	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE users SET totp_enabled=FALSE, totp_secret='', pending_totp_secret='',
		        pending_totp_nonce='', pending_totp_expires_at=NULL, updated_at=now() WHERE id=$1`,
		u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Zwei-Faktor konnte nicht deaktiviert werden")
		return
	}
	h.Auth.DeleteBackupCodes(r.Context(), u.ID)
	h.auditChange(r, "update", "user", u.ID, u.Username+" disabled two-factor authentication",
		diffFields(map[string]any{"totp_enabled": true}, map[string]any{"totp_enabled": false}))
	writeJSON(w, http.StatusOK, map[string]string{"status": "disabled"})
}

// TOTPBackupCount returns how many unused recovery codes remain.
func (h *AuthHandler) TOTPBackupCount(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	n, err := h.Auth.RemainingBackupCodes(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"remaining": n, "enabled": u.TOTPEnabled})
}

// TOTPRegenerateBackup issues a fresh set of recovery codes (invalidating the
// old ones) after re-authenticating with the password. 2FA must be enabled.
func (h *AuthHandler) TOTPRegenerateBackup(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	if !u.TOTPEnabled {
		writeError(w, http.StatusConflict, "Zwei-Faktor ist nicht aktiviert")
		return
	}
	var req totpDisableRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// An over-long password can't match (bcrypt caps at 72 bytes), so
	// reject it up front rather than spending a bcrypt compare on it.
	if len(req.Password) > maxPasswordLen {
		writeError(w, http.StatusForbidden, "Passwort ist falsch")
		return
	}

	key, ip, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}

	if _, err := h.Auth.AuthenticateUserID(r.Context(), u.ID, req.Password); err != nil {
		h.recordReauthFailure(key, ip)
		writeError(w, http.StatusForbidden, "Passwort ist falsch")
		return
	}
	h.resetReauth(key, ip)

	codes, err := h.Auth.GenerateBackupCodes(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate recovery codes")
		return
	}
	h.audit(r, "update", "user", u.ID, u.Username+" regenerated recovery codes")
	writeJSON(w, http.StatusOK, map[string]any{"backup_codes": codes})
}
