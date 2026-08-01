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
	// Throttle: a 6-digit code is otherwise brute-forceable during enrolment.
	key, _, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}
	if !h.Auth.ValidateEncryptedTOTP(encSecret, trim(req.Code)) {
		h.Limiter.RecordFailure(key)
		writeError(w, http.StatusBadRequest, "invalid code")
		return
	}
	h.Limiter.Reset(key)
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
	h.audit(r, "update", "user", u.ID, "enabled two-factor authentication")
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

	key, _, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}

	if _, err := h.Auth.Authenticate(r.Context(), u.Username, req.Password); err != nil {
		h.Limiter.RecordFailure(key)
		writeError(w, http.StatusForbidden, "password is incorrect")
		return
	}
	h.Limiter.Reset(key)

	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE users SET totp_enabled=FALSE, totp_secret='', updated_at=now() WHERE id=$1`,
		u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not disable two-factor")
		return
	}
	h.Auth.DeleteBackupCodes(r.Context(), u.ID)
	h.audit(r, "update", "user", u.ID, u.Username+" disabled two-factor authentication")
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

	key, _, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}

	if _, err := h.Auth.Authenticate(r.Context(), u.Username, req.Password); err != nil {
		h.Limiter.RecordFailure(key)
		writeError(w, http.StatusForbidden, "password is incorrect")
		return
	}
	h.Limiter.Reset(key)

	codes, err := h.Auth.GenerateBackupCodes(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate recovery codes")
		return
	}
	h.audit(r, "update", "user", u.ID, u.Username+" regenerated recovery codes")
	writeJSON(w, http.StatusOK, map[string]any{"backup_codes": codes})
}
