package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// AuthHandler wires authentication endpoints to the auth Manager.
type AuthHandler struct {
	*Handler
	Auth     *auth.Manager
	WebAuthn *auth.WebAuthnService // nil when passkeys are disabled
	Limiter  *auth.LoginLimiter
}

// NewAuthHandler constructs an AuthHandler. The background login-throttle
// cleanup goroutine runs until stop is closed.
func NewAuthHandler(h *Handler, mgr *auth.Manager, wa *auth.WebAuthnService, stop <-chan struct{}) *AuthHandler {
	ah := &AuthHandler{
		Handler:  h,
		Auth:     mgr,
		WebAuthn: wa,
		Limiter:  auth.NewLoginLimiter(5, 10*time.Minute, 15*time.Minute),
	}
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				ah.Limiter.Cleanup()
			}
		}
	}()
	return ah
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

// checkRateLimit blocks if the username+IP is currently throttled.
func (h *AuthHandler) checkRateLimit(w http.ResponseWriter, r *http.Request, username string) (string, bool) {
	ip := h.Auth.ClientIP(r)
	// Usernames are case-insensitive in the database; ensure the throttle
	// key reflects this so variations in casing cannot bypass the lockout.
	key := strings.ToLower(username) + "|" + ip
	if ok, wait := h.Limiter.Allowed(key); !ok {
		w.Header().Set("Retry-After", formatSeconds(wait))
		slog.Warn("throttle active", "user", username, "ip", ip, "path", r.URL.Path)
		writeError(w, http.StatusTooManyRequests,
			"too many attempts, try again in "+formatMinutes(wait))
		return key, false
	}
	return key, true
}

// Login authenticates a user (with optional TOTP) and starts a session.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Username = trim(req.Username)
	key, ok := h.checkRateLimit(w, r, req.Username)
	if !ok {
		return
	}

	u, err := h.Auth.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		h.Limiter.RecordFailure(key)
		slog.Warn("login failed", "user", req.Username, "ip", h.Auth.ClientIP(r), "reason", "bad credentials")
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	// Second factor: authenticator code or a one-time backup code.
	if u.TOTPEnabled {
		code := trim(req.TOTPCode)
		if code == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "two-factor code required",
				"totp_required": true,
			})
			return
		}
		ok := h.Auth.ValidateEncryptedTOTP(u.TOTPSecret, code)
		if !ok {
			ok = h.Auth.ConsumeBackupCode(r.Context(), u.ID, code)
		}
		if !ok {
			h.Limiter.RecordFailure(key)
			slog.Warn("login failed", "user", u.Username, "ip", h.Auth.ClientIP(r), "reason", "bad 2fa code")
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "invalid two-factor code",
				"totp_required": true,
			})
			return
		}
	}

	h.Limiter.Reset(key)
	if err := h.Auth.CreateSession(r.Context(), w, r, u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	slog.Info("login", "user", u.Username, "user_id", u.ID, "ip", h.Auth.ClientIP(r),
		"role", u.Role, "twofactor", u.TOTPEnabled)
	h.auditAs(r, u.ID, u.Username, "login", "user", u.ID, u.Username+" signed in")
	writeJSON(w, http.StatusOK, u)
}

// Logout ends the current session.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if u, ok := auth.UserFrom(r.Context()); ok {
		slog.Info("logout", "user", u.Username, "user_id", u.ID, "ip", h.Auth.ClientIP(r))
		h.audit(r, "logout", "user", u.ID, u.Username+" signed out")
	}
	h.Auth.DestroySession(r.Context(), w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Me returns the currently authenticated user.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword lets a signed-in user change their own password.
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	var req changePasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validPasswordLength(req.NewPassword) {
		writeError(w, http.StatusBadRequest, "new password must be between 8 and 72 bytes")
		return
	}

	// Gate on the rate limiter and prove knowledge of the current password
	// (cheap, local bcrypt) BEFORE spending an outbound HIBP breach lookup on
	// the candidate password — otherwise the endpoint drives external requests
	// from arbitrary input before any throttle applies.
	key, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}
	if _, err := h.Auth.Authenticate(r.Context(), u.Username, req.CurrentPassword); err != nil {
		h.Limiter.RecordFailure(key)
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	h.Limiter.Reset(key)

	if h.passwordBreached(r.Context(), req.NewPassword) {
		writeError(w, http.StatusBadRequest,
			"this password has appeared in a known data breach; please choose another")
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE users SET password_hash=$1, updated_at=now() WHERE id=$2`, hash, u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update password")
		return
	}
	// Terminate every existing session and bind the new password to a fresh
	// session + CSRF token (signs out other devices, defeats fixation).
	if err := h.Auth.RotateSession(r.Context(), w, r, u.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not refresh session")
		return
	}
	h.audit(r, "update", "user", u.ID, "changed own password")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Sessions management ---

// ListSessions returns the current user's active sessions.
func (h *AuthHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	sessions, err := h.Auth.ListSessions(r.Context(), u.ID, auth.CurrentToken(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

// RevokeSession deletes one of the current user's sessions by handle.
func (h *AuthHandler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	handle := r.PathValue("handle")
	n, err := h.Auth.RevokeSession(r.Context(), u.ID, handle)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not revoke session")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// RevokeOtherSessions signs the user out everywhere except the current session.
func (h *AuthHandler) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	if err := h.Auth.RevokeOtherSessions(r.Context(), u.ID, auth.CurrentToken(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not revoke sessions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func formatSeconds(d time.Duration) string {
	s := int(d.Seconds())
	if s < 1 {
		s = 1
	}
	return strconv.Itoa(s)
}

func formatMinutes(d time.Duration) string {
	m := int(d.Minutes()) + 1
	if m <= 1 {
		return "about a minute"
	}
	return strconv.Itoa(m) + " minutes"
}
