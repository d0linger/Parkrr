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
	// IPLimiter throttles failures per client IP regardless of username, so
	// password-spraying (one guess against many accounts from one host) trips a
	// lockout even though each username+IP key stays under the per-account
	// threshold. Its cap is higher to tolerate a few users behind one NAT.
	IPLimiter *auth.LoginLimiter
}

// NewAuthHandler constructs an AuthHandler. The background login-throttle
// cleanup goroutine runs until stop is closed.
func NewAuthHandler(h *Handler, mgr *auth.Manager, wa *auth.WebAuthnService, stop <-chan struct{}) *AuthHandler {
	ah := &AuthHandler{
		Handler:   h,
		Auth:      mgr,
		WebAuthn:  wa,
		Limiter:   auth.NewLoginLimiter(5, 10*time.Minute, 15*time.Minute),
		IPLimiter: auth.NewLoginLimiter(20, 10*time.Minute, 15*time.Minute),
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
				ah.IPLimiter.Cleanup()
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

// checkRateLimit blocks if the username+IP key is currently throttled. It
// returns the username+IP key and the client IP for recording the outcome.
// Usernames are lower-cased so casing variants can't bypass the lockout. The
// per-IP spray throttle is applied only on the public login path (ipThrottled),
// not on post-auth endpoints that also use this helper.
func (h *AuthHandler) checkRateLimit(w http.ResponseWriter, r *http.Request, username string) (key, ip string, ok bool) {
	ip = h.Auth.ClientIP(r)
	key = strings.ToLower(username) + "|" + ip
	if allowed, wait := h.Limiter.Allowed(key); !allowed {
		w.Header().Set("Retry-After", formatSeconds(wait))
		slog.Warn("throttle active", "user", username, "ip", ip, "path", r.URL.Path)
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again in "+formatMinutes(wait))
		return key, ip, false
	}
	return key, ip, true
}

// ipThrottled blocks (and 429s) when the client IP has tripped the per-IP
// spray throttle. Used only on the public login endpoint so that one host
// spraying one password across many usernames trips a lockout even though no
// single username+IP key reaches its own threshold.
func (h *AuthHandler) ipThrottled(w http.ResponseWriter, r *http.Request) (ip string, blocked bool) {
	ip = h.Auth.ClientIP(r)
	if allowed, wait := h.IPLimiter.Allowed(ip); !allowed {
		w.Header().Set("Retry-After", formatSeconds(wait))
		slog.Warn("throttle active (ip)", "ip", ip, "path", r.URL.Path)
		writeError(w, http.StatusTooManyRequests, "too many attempts, try again in "+formatMinutes(wait))
		return ip, true
	}
	return ip, false
}

// recordLoginFailure counts a failed login against both the per-account and the
// per-IP throttle. Called only from the public login path.
func (h *AuthHandler) recordLoginFailure(key, ip string) {
	h.Limiter.RecordFailure(key)
	h.IPLimiter.RecordFailure(ip)
}

// Login authenticates a user (with optional TOTP) and starts a session.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Username = trim(req.Username)
	// Reject over-long identifiers/credentials before any state lookup: caps
	// the rate-limiter key size and avoids handing bcrypt an oversized input.
	if !validUsernameLength(req.Username) || len(req.Password) > maxPasswordLen || len(req.TOTPCode) > maxTOTPCodeLen {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	// Per-IP spray throttle first (independent of username), then the per-account
	// lockout. Both apply only to this public login endpoint.
	if _, blocked := h.ipThrottled(w, r); blocked {
		return
	}
	key, ip, ok := h.checkRateLimit(w, r, req.Username)
	if !ok {
		return
	}

	u, err := h.Auth.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		h.recordLoginFailure(key, ip)
		slog.Warn("login failed", "user", req.Username, "ip", ip, "reason", "bad credentials")
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
			h.recordLoginFailure(key, ip)
			slog.Warn("login failed", "user", u.Username, "ip", ip, "reason", "bad 2fa code")
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "invalid two-factor code",
				"totp_required": true,
			})
			return
		}
	}

	// Reset only the per-account key on success. The per-IP spray counter is
	// deliberately NOT reset here: otherwise one legitimate login from a shared
	// egress IP would hand a co-located sprayer a fresh budget. It decays on its
	// own via the failure window.
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
	// Enforce rotation: the new password must actually change. Rejecting an
	// identical new password stops a forced reset from being no-op'd by
	// resubmitting the current one.
	if req.NewPassword == req.CurrentPassword {
		writeError(w, http.StatusBadRequest, "new password must differ from the current password")
		return
	}
	// An over-long current password can't match (bcrypt caps at 72 bytes), so
	// reject it up front rather than spending a bcrypt compare on it.
	if len(req.CurrentPassword) > maxPasswordLen {
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}

	// Gate on the rate limiter and prove knowledge of the current password
	// (cheap, local bcrypt) BEFORE spending an outbound HIBP breach lookup on
	// the candidate password — otherwise the endpoint drives external requests
	// from arbitrary input before any throttle applies.
	key, _, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}
	if _, err := h.Auth.Authenticate(r.Context(), u.Username, req.CurrentPassword); err != nil {
		h.Limiter.RecordFailure(key)
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	h.Limiter.Reset(key)

	if h.rejectBreachedPassword(w, r, req.NewPassword) {
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
