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
	//
	// Known limitation (accepted): both counters are keyed on the client IP, so an
	// attacker who rotates source addresses (botnet, proxy pool) evades them. That
	// is out of scope for an in-memory single-instance throttle; broader
	// credential-stuffing defense (breached-password checks, per-account lockout,
	// 2FA, passkeys) is layered on top and does not depend on the source IP.
	IPLimiter *auth.LoginLimiter
	// UserLimiter throttles failures per username REGARDLESS of IP, closing the
	// IP-rotation gap: a distributed attacker guessing one account from many source
	// addresses is bounded to ~20 tries / 15 min. The cooldown is deliberately SHORT
	// (1 min) so that this account-wide counter can't be weaponized to lock a real
	// user out for long — it slows brute force without becoming a lockout-DoS.
	UserLimiter *auth.LoginLimiter
}

// NewAuthHandler constructs an AuthHandler. The background login-throttle
// cleanup goroutine runs until stop is closed.
func NewAuthHandler(h *Handler, mgr *auth.Manager, wa *auth.WebAuthnService, stop <-chan struct{}) *AuthHandler {
	ah := &AuthHandler{
		Handler:     h,
		Auth:        mgr,
		WebAuthn:    wa,
		Limiter:     auth.NewLoginLimiter(5, 10*time.Minute, 15*time.Minute),
		IPLimiter:   auth.NewLoginLimiter(20, 10*time.Minute, 15*time.Minute),
		UserLimiter: auth.NewStickyLoginLimiter(20, 15*time.Minute, 1*time.Minute),
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
				ah.UserLimiter.Cleanup()
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
	uname := strings.ToLower(username)
	key = uname + "|" + ip
	if allowed, wait := h.Limiter.Allowed(key); !allowed {
		w.Header().Set("Retry-After", formatSeconds(wait))
		// Don't log the request-supplied username (clear-text-logging / PII): a
		// throttle event is identified by IP + path; the account isn't needed here.
		slog.Warn("throttle active", "ip", ip, "path", r.URL.Path)
		writeError(w, http.StatusTooManyRequests, "Zu viele Versuche – bitte in "+formatMinutes(wait)+" erneut versuchen")
		return key, ip, false
	}
	// Per-username (IP-independent) throttle — bounds distributed brute force.
	if allowed, wait := h.UserLimiter.Allowed(uname); !allowed {
		w.Header().Set("Retry-After", formatSeconds(wait))
		slog.Warn("throttle active (user)", "ip", ip, "path", r.URL.Path)
		writeError(w, http.StatusTooManyRequests, "Zu viele Versuche – bitte in "+formatMinutes(wait)+" erneut versuchen")
		return key, ip, false
	}
	return key, ip, true
}

// userKeyOf recovers the per-username throttle key (lower-cased username) from the
// combined "username|ip" key, by stripping the exact "|ip" suffix.
func userKeyOf(key, ip string) string { return strings.TrimSuffix(key, "|"+ip) }

// ipThrottled blocks (and 429s) when the client IP has tripped the per-IP
// spray throttle. Used only on the public login endpoint so that one host
// spraying one password across many usernames trips a lockout even though no
// single username+IP key reaches its own threshold.
func (h *AuthHandler) ipThrottled(w http.ResponseWriter, r *http.Request) bool {
	ip := h.Auth.ClientIP(r)
	if allowed, wait := h.IPLimiter.Allowed(ip); !allowed {
		w.Header().Set("Retry-After", formatSeconds(wait))
		slog.Warn("throttle active (ip)", "ip", ip, "path", r.URL.Path)
		writeError(w, http.StatusTooManyRequests, "Zu viele Versuche – bitte in "+formatMinutes(wait)+" erneut versuchen")
		return true
	}
	return false
}

// recordLoginFailure counts a failed login against both the per-account and the
// per-IP throttle. Called only from the public login path.
func (h *AuthHandler) recordLoginFailure(key, ip string) {
	h.Limiter.RecordFailure(key)
	h.IPLimiter.RecordFailure(ip)
	h.UserLimiter.RecordFailure(userKeyOf(key, ip))
}

// recordReauthFailure counts a failed re-authentication on an authenticated
// account-management endpoint (2FA enable/disable, backup-code regeneration)
// against BOTH the username|ip limiter and the per-account (IP-independent)
// limiter. checkRateLimit already CHECKS both, so recording only the username|ip
// counter let a distributed brute force rotate IPs and never trip the per-account
// lockout (finding P-02). The per-IP spray limiter is login-only and not touched.
func (h *AuthHandler) recordReauthFailure(key, ip string) {
	h.Limiter.RecordFailure(key)
	h.UserLimiter.RecordFailure(userKeyOf(key, ip))
}

// resetReauth clears both counters after a successful re-authentication.
func (h *AuthHandler) resetReauth(key, ip string) {
	h.Limiter.Reset(key)
	h.UserLimiter.Reset(userKeyOf(key, ip))
}

// stepUpWindow is how long after a primary-factor login (password or passkey) a
// sensitive account change — adding a second factor — is allowed without
// re-authenticating (finding SH-02).
const stepUpWindow = 10 * time.Minute

// requireStepUp gates a sensitive change. It passes when the session
// authenticated within stepUpWindow; otherwise it requires the account password.
// A missing password yields 403 "reauth_required" so the client can prompt and
// retry with the password; a wrong password is throttled like a login failure.
// Returns false (and writes the response) when the caller must stop.
func (h *AuthHandler) requireStepUp(w http.ResponseWriter, r *http.Request, username, password string) bool {
	if t, ok := h.Auth.SessionCreatedAt(r.Context(), r); ok && time.Since(t) < stepUpWindow {
		return true
	}
	if password == "" {
		writeError(w, http.StatusForbidden, "reauth_required")
		return false
	}
	if len(password) > maxPasswordLen {
		writeError(w, http.StatusForbidden, "Passwort ist falsch")
		return false
	}
	key, ip, ok := h.checkRateLimit(w, r, username)
	if !ok {
		return false
	}
	if _, err := h.Auth.Authenticate(r.Context(), username, password); err != nil {
		h.recordReauthFailure(key, ip)
		writeError(w, http.StatusForbidden, "Passwort ist falsch")
		return false
	}
	// Do NOT reset the limiter here: TOTPEnable shares this key with its TOTP-code
	// throttle, and clearing it on a successful step-up password would wipe the
	// code-attempt budget before the code itself succeeds. The caller resets only
	// after the whole operation succeeds (finding: twofactor step-up reset).
	return true
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
		writeError(w, http.StatusUnauthorized, "Benutzername oder Passwort ist falsch")
		return
	}
	// Per-IP spray throttle first (independent of username), then the per-account
	// lockout. Both apply only to this public login endpoint.
	if h.ipThrottled(w, r) {
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
		writeError(w, http.StatusUnauthorized, "Benutzername oder Passwort ist falsch")
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
		ok := false
		if step, matched := h.Auth.ValidateEncryptedTOTPStep(u.TOTPSecret, code, time.Now()); matched {
			// Replay guard: consume the code by advancing last_totp_step atomically.
			// A code from an already-used (or older) step affects no row -> rejected.
			ct, uerr := h.Pool.Exec(r.Context(),
				`UPDATE users SET last_totp_step=$1 WHERE id=$2 AND last_totp_step < $1`, step, u.ID)
			if uerr != nil {
				// Fail closed on a DB error rather than mislabelling a valid code as a
				// replay and counting it toward the account lockout.
				writeError(w, http.StatusInternalServerError, "Zwei-Faktor-Code konnte nicht geprüft werden")
				return
			}
			if ct.RowsAffected() == 1 {
				ok = true
			} else {
				slog.Warn("login failed", "user", u.Username, "ip", ip, "reason", "2fa code replay")
			}
		}
		if !ok {
			ok = h.Auth.ConsumeBackupCode(r.Context(), u.ID, code)
		}
		if !ok {
			h.recordLoginFailure(key, ip)
			slog.Warn("login failed", "user", u.Username, "ip", ip, "reason", "bad 2fa code")
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "Zwei-Faktor-Code ist ungültig",
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
	h.UserLimiter.Reset(userKeyOf(key, ip))
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
		writeError(w, http.StatusBadRequest, "Das neue Passwort muss zwischen 8 und 72 Zeichen lang sein (Umlaute und Sonderzeichen zählen doppelt)")
		return
	}
	// Enforce rotation: the new password must actually change. Rejecting an
	// identical new password stops a forced reset from being no-op'd by
	// resubmitting the current one.
	if req.NewPassword == req.CurrentPassword {
		writeError(w, http.StatusBadRequest, "Das neue Passwort muss sich vom aktuellen unterscheiden")
		return
	}
	// An over-long current password can't match (bcrypt caps at 72 bytes), so
	// reject it up front rather than spending a bcrypt compare on it.
	if len(req.CurrentPassword) > maxPasswordLen {
		writeError(w, http.StatusForbidden, "Aktuelles Passwort ist falsch")
		return
	}

	// Gate on the rate limiter and prove knowledge of the current password
	// (cheap, local bcrypt) BEFORE spending an outbound HIBP breach lookup on
	// the candidate password — otherwise the endpoint drives external requests
	// from arbitrary input before any throttle applies.
	key, ip, ok := h.checkRateLimit(w, r, u.Username)
	if !ok {
		return
	}
	if _, err := h.Auth.Authenticate(r.Context(), u.Username, req.CurrentPassword); err != nil {
		// checkRateLimit gates this flow on BOTH limiters, so feed both — a wrong
		// current password counts against the per-account throttle too. recordReauth-
		// Failure trifft Limiter + UserLimiter (nicht den login-only IPLimiter): ein
		// Passwortwechsel ist eine Re-Auth, kein Login (PR #125).
		h.recordReauthFailure(key, ip)
		writeError(w, http.StatusForbidden, "Aktuelles Passwort ist falsch")
		return
	}
	h.Limiter.Reset(key)
	h.UserLimiter.Reset(userKeyOf(key, ip))

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
	// Revoking a session is a security action: it is how a stolen session is cut off,
	// and equally how an attacker would cut off the legitimate owner.
	//
	// The handle is deliberately NOT recorded. It is left(token, 8) — the first eight
	// characters of the real session token (see auth/sessions.go) — so writing it here
	// would persist live credential material into an append-only table kept for years,
	// and `session_handle` matches no secret token, so the redaction choke point would
	// not catch it. The entity is the USER, which is the object entity_id identifies;
	// there is no session row id to point at once the row is deleted.
	h.auditChange(r, "revoke", "user", u.ID, u.Username+" revoked a session",
		auditSnapshot(map[string]any{"revoked_sessions": n}))
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// RevokeOtherSessions signs the user out everywhere except the current session.
func (h *AuthHandler) RevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	if err := h.Auth.RevokeOtherSessions(r.Context(), u.ID, auth.CurrentToken(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not revoke sessions")
		return
	}
	// Signing out every other device is the standard follow-up to a suspected
	// compromise — and the standard move of whoever caused it. It must be in the trail.
	h.auditChange(r, "revoke", "user", u.ID, u.Username+" signed out all other sessions",
		auditSnapshot(map[string]any{"scope": "all_other_sessions"}))
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
		return "etwa einer Minute"
	}
	return strconv.Itoa(m) + " Minuten"
}
