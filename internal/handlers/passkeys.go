package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// waCookie holds the short-lived, encrypted WebAuthn ceremony state (challenge)
// between the begin and finish steps.
const waCookie = "parkrr_wa"

type waCeremony struct {
	Session webauthn.SessionData `json:"s"`
	Name    string               `json:"n,omitempty"`
}

// Capabilities reports optional features to the (possibly unauthenticated)
// client so the UI can adapt — currently whether passkeys are available.
func (h *AuthHandler) Capabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"passkeys": h.WebAuthn != nil && h.WebAuthn.Enabled(),
	})
}

func (h *AuthHandler) passkeysEnabled(w http.ResponseWriter) bool {
	if h.WebAuthn == nil || !h.WebAuthn.Enabled() {
		writeError(w, http.StatusNotFound, "passkeys are not enabled")
		return false
	}
	return true
}

func (h *AuthHandler) storeCeremony(w http.ResponseWriter, sd webauthn.SessionData, name string) error {
	blob, err := json.Marshal(waCeremony{Session: sd, Name: name})
	if err != nil {
		return err
	}
	sealed, err := h.Auth.Seal(string(blob))
	if err != nil {
		return err
	}
	// Secure is unconditionally true: WebAuthn only runs in a secure context
	// (HTTPS, or http://localhost which browsers treat as secure and still send
	// Secure cookies to), so this cookie never needs to work over plain HTTP.
	http.SetCookie(w, &http.Cookie{
		Name: waCookie, Value: sealed, Path: "/", MaxAge: 300,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (h *AuthHandler) loadCeremony(r *http.Request) (waCeremony, error) {
	var cer waCeremony
	c, err := r.Cookie(waCookie)
	if err != nil || c.Value == "" {
		return cer, errors.New("no ceremony in progress")
	}
	plain, err := h.Auth.Open(c.Value)
	if err != nil {
		return cer, err
	}
	return cer, json.Unmarshal([]byte(plain), &cer)
}

func (h *AuthHandler) clearCeremony(w http.ResponseWriter) {
	// Deletion cookie mirroring how the ceremony cookie was set (Secure always on).
	http.SetCookie(w, &http.Cookie{
		Name: waCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// --- Registration (authenticated) ---

// PasskeyRegisterBegin returns credential-creation options for enrolling a new
// passkey and stashes the challenge in an encrypted cookie.
func (h *AuthHandler) PasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if !h.passkeysEnabled(w) {
		return
	}
	u, _ := auth.UserFrom(r.Context())
	var body struct {
		Name string `json:"name"`
	}
	_ = decodeJSON(r, &body) // name is optional

	body.Name = trim(body.Name)
	if !validNameLength(body.Name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}

	opts, sd, err := h.WebAuthn.BeginRegistration(r.Context(), u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start passkey registration")
		return
	}
	if err := h.storeCeremony(w, *sd, body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start passkey registration")
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

// PasskeyRegisterFinish verifies the attestation and stores the credential.
func (h *AuthHandler) PasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if !h.passkeysEnabled(w) {
		return
	}
	u, _ := auth.UserFrom(r.Context())
	// Throttle: cap repeated failed attestation verifications (CPU-heavy) per
	// user, matching the TOTP-enable ceremony.
	key, _, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}
	cer, err := h.loadCeremony(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "passkey registration expired, please retry")
		return
	}
	h.clearCeremony(w)
	if err := h.WebAuthn.FinishRegistration(r.Context(), u, cer.Session, r, cer.Name); err != nil {
		// Only a real verification failure counts toward the throttle; a backend
		// error is transient and must not lock the user out.
		if errors.Is(err, auth.ErrWebAuthnInternal) {
			slog.Error("passkey registration backend error", "user", u.Username, "err", err)
			writeError(w, http.StatusInternalServerError, "could not register passkey")
			return
		}
		h.Limiter.RecordFailure(key)
		slog.Warn("passkey registration failed", "user", u.Username, "err", err)
		writeError(w, http.StatusBadRequest, "could not verify passkey")
		return
	}
	h.Limiter.Reset(key)
	h.audit(r, "create", "passkey", u.ID, u.Username+" registered a passkey")
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

// --- Listing / deletion (authenticated) ---

// ListPasskeys returns the current user's registered passkeys.
func (h *AuthHandler) ListPasskeys(w http.ResponseWriter, r *http.Request) {
	if h.WebAuthn == nil || !h.WebAuthn.Enabled() {
		writeJSON(w, http.StatusOK, []auth.CredentialInfo{})
		return
	}
	u, _ := auth.UserFrom(r.Context())
	creds, err := h.WebAuthn.ListCredentials(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, creds)
}

// DeletePasskey removes one of the current user's passkeys.
func (h *AuthHandler) DeletePasskey(w http.ResponseWriter, r *http.Request) {
	if !h.passkeysEnabled(w) {
		return
	}
	u, _ := auth.UserFrom(r.Context())
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	n, err := h.WebAuthn.DeleteCredential(r.Context(), u.ID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete passkey")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "passkey not found")
		return
	}
	h.audit(r, "delete", "passkey", id, u.Username+" removed a passkey")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Login (public) ---

// throttlePasskeyLogin enforces the shared login throttle on the usernameless
// passkey login endpoints, keyed by client IP (there is no username to key on).
// It mirrors the password-login lockout so repeated failed assertions — or a
// locked-out client hammering the challenge endpoint — are rejected with a 429,
// on top of the coarse global per-IP rate limit. Failures are recorded by
// PasskeyLoginFinish; a successful login resets the counter.
func (h *AuthHandler) throttlePasskeyLogin(w http.ResponseWriter, r *http.Request) (string, bool) {
	ip := h.Auth.ClientIP(r)
	key := "passkey|" + ip
	if ok, wait := h.Limiter.Allowed(key); !ok {
		w.Header().Set("Retry-After", formatSeconds(wait))
		slog.Warn("passkey throttle active", "ip", ip, "path", r.URL.Path)
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again in "+formatMinutes(wait))
		return key, false
	}
	return key, true
}

// PasskeyLoginBegin returns assertion options for a usernameless passkey login.
func (h *AuthHandler) PasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !h.passkeysEnabled(w) {
		return
	}
	if _, ok := h.throttlePasskeyLogin(w, r); !ok {
		return
	}
	opts, sd, err := h.WebAuthn.BeginLogin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start passkey login")
		return
	}
	if err := h.storeCeremony(w, *sd, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "could not start passkey login")
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

// PasskeyLoginFinish verifies the assertion and starts a session.
func (h *AuthHandler) PasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if !h.passkeysEnabled(w) {
		return
	}
	key, ok := h.throttlePasskeyLogin(w, r)
	if !ok {
		return
	}
	cer, err := h.loadCeremony(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "passkey login expired, please retry")
		return
	}
	h.clearCeremony(w)

	uid, err := h.WebAuthn.FinishLogin(r.Context(), cer.Session, r)
	if err != nil {
		// A backend error is not a brute-force signal; don't spend the throttle
		// budget on it and surface it as a server error.
		if errors.Is(err, auth.ErrWebAuthnInternal) {
			slog.Error("passkey login backend error", "ip", h.Auth.ClientIP(r), "err", err)
			writeError(w, http.StatusInternalServerError, "passkey login failed")
			return
		}
		h.Limiter.RecordFailure(key)
		slog.Warn("passkey login failed", "ip", h.Auth.ClientIP(r), "err", err)
		writeError(w, http.StatusUnauthorized, "passkey login failed")
		return
	}
	u, err := h.userByID(r.Context(), uid)
	if err != nil {
		// The assertion already verified cryptographically; a failure here is a
		// DB/stale-credential issue, not a brute-force attempt, so it must not
		// spend the IP's throttle budget — just log it.
		slog.Warn("passkey login: user lookup failed after verification", "user_id", uid, "err", err)
		writeError(w, http.StatusUnauthorized, "passkey login failed")
		return
	}
	h.Limiter.Reset(key)
	if err := h.Auth.CreateSession(r.Context(), w, r, uid); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	slog.Info("login", "user", u.Username, "user_id", uid, "ip", h.Auth.ClientIP(r),
		"role", u.Role, "method", "passkey")
	h.auditAs(r, uid, u.Username, "login", "user", uid, u.Username+" signed in (passkey)")
	writeJSON(w, http.StatusOK, u)
}

// userByID loads the fields needed for a session/response.
func (h *AuthHandler) userByID(ctx context.Context, id int64) (*models.User, error) {
	var u models.User
	err := h.Pool.QueryRow(ctx,
		`SELECT id, username, email, is_admin, role, totp_enabled, created_at, updated_at
		 FROM users WHERE id=$1`, id).Scan(&u.ID, &u.Username, &u.Email, &u.IsAdmin, &u.Role,
		&u.TOTPEnabled, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
