package handlers

import (
	"net/http"

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
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE users SET totp_secret=$1, totp_enabled=FALSE, updated_at=now() WHERE id=$2`,
		encSecret, u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store secret")
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
	})
}

type totpVerifyRequest struct {
	Code string `json:"code"`
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
	var encSecret string
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT totp_secret FROM users WHERE id=$1`, u.ID).Scan(&encSecret); err != nil || encSecret == "" {
		writeError(w, http.StatusBadRequest, "start setup first")
		return
	}
	if !validTOTPCodeLength(trim(req.Code)) {
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	// Step-up: enabling a second factor requires a recent primary-factor login,
	// or the account password if that window has closed (finding SH-02).
	if !h.requireStepUp(w, r, u.Username, req.Password) {
		return
	}
	// Throttle: a 6-digit code is otherwise brute-forceable during enrolment.
	key, ip, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}
	if !h.Auth.ValidateEncryptedTOTP(encSecret, trim(req.Code)) {
		h.recordReauthFailure(key, ip)
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE users SET totp_enabled=TRUE, updated_at=now() WHERE id=$1`, u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not enable two-factor")
		return
	}
	// Issue one-time backup codes (shown once).
	codes, err := h.Auth.GenerateBackupCodes(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate backup codes")
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
		writeError(w, http.StatusForbidden, "password is incorrect")
		return
	}

	key, ip, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}

	if _, err := h.Auth.Authenticate(r.Context(), u.Username, req.Password); err != nil {
		h.recordReauthFailure(key, ip)
		writeError(w, http.StatusForbidden, "password is incorrect")
		return
	}
	h.resetReauth(key, ip)

	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE users SET totp_enabled=FALSE, totp_secret='', updated_at=now() WHERE id=$1`,
		u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not disable two-factor")
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
		writeError(w, http.StatusConflict, "two-factor is not enabled")
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
		writeError(w, http.StatusForbidden, "password is incorrect")
		return
	}

	key, ip, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}

	if _, err := h.Auth.Authenticate(r.Context(), u.Username, req.Password); err != nil {
		h.recordReauthFailure(key, ip)
		writeError(w, http.StatusForbidden, "password is incorrect")
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
