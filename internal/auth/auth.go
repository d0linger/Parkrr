// Package auth handles password hashing, session management and middleware.
package auth

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/preining/parkrr/internal/models"
)

const (
	// SessionCookie is the name of the session cookie.
	SessionCookie = "parkrr_session"
	// CSRFCookie is the name of the CSRF token cookie (readable by JS).
	CSRFCookie = "parkrr_csrf"
	// CSRFHeader is the request header carrying the CSRF token.
	CSRFHeader = "X-CSRF-Token"
)

// ctxKey is a private type for context keys to avoid collisions.
type ctxKey int

const userCtxKey ctxKey = 0

// Manager provides authentication services backed by Postgres.
type Manager struct {
	pool           *pgxpool.Pool
	sessionMaxAge  time.Duration
	sliding        bool          // renew session expiry on activity
	absoluteMaxAge time.Duration // hard cap on total session lifetime (sliding mode)
	secureCookies  bool
	trustProxy     bool
	aead           cipher.AEAD
}

// SessionConfig groups the session-lifetime settings for NewManager.
type SessionConfig struct {
	MaxAge         int // seconds (idle window when sliding, else absolute)
	Sliding        bool
	AbsoluteMaxAge int // seconds; hard cap when sliding
}

// NewManager constructs an auth Manager. secret is used to derive the key that
// encrypts TOTP secrets at rest.
func NewManager(pool *pgxpool.Pool, sc SessionConfig, secureCookies, trustProxy bool, secret string) (*Manager, error) {
	aead, err := newAEAD(secret)
	if err != nil {
		return nil, err
	}
	return &Manager{
		pool:           pool,
		sessionMaxAge:  time.Duration(sc.MaxAge) * time.Second,
		sliding:        sc.Sliding,
		absoluteMaxAge: time.Duration(sc.AbsoluteMaxAge) * time.Second,
		secureCookies:  secureCookies,
		trustProxy:     trustProxy,
		aead:           aead,
	}, nil
}

// hashToken returns the hex SHA-256 of a raw token. Session tokens are stored
// hashed at rest so a database leak does not expose usable session cookies.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ClientIP returns the best-effort client IP. Forwarded headers are only
// trusted when the app is configured to run behind a trusted reverse proxy.
func (m *Manager) ClientIP(r *http.Request) string {
	if m.trustProxy {
		// Join ALL X-Forwarded-For header lines: a client may send its own
		// XFF and the proxy may append its hop as a separate header field, so
		// Header.Get (first line only) would miss the proxy-added value.
		if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
			// Use the RIGHTMOST entry: that is the address our own trusted proxy
			// appended. Everything to the left is client-supplied and forgeable,
			// so keying the rate limiter off the leftmost hop would let an
			// attacker rotate X-Forwarded-For to dodge the lockout.
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RequestIsHTTPS reports whether the original client request used HTTPS,
// honoring X-Forwarded-Proto only behind a trusted proxy.
func (m *Manager) RequestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if m.trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

// cookieSecure decides the Secure flag for cookies on this request.
func (m *Manager) cookieSecure(r *http.Request) bool {
	return m.secureCookies || m.RequestIsHTTPS(r)
}

// HashPassword returns a bcrypt hash of the given plaintext password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword verifies a plaintext password against a bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummyHash is a valid bcrypt hash at the default cost. When a login names an
// unknown user we still compare the supplied password against this hash so the
// request spends the same time as a real bcrypt verification, denying an
// attacker a timing oracle for user enumeration. Computed once at startup.
var dummyHash = mustBcryptHash("parkrr-timing-equalizer")

func mustBcryptHash(s string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(s), bcrypt.DefaultCost)
	if err != nil {
		panic("bcrypt init: " + err.Error())
	}
	return h
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Authenticate validates credentials and returns the matching user.
func (m *Manager) Authenticate(ctx context.Context, username, password string) (*models.User, error) {
	u, err := m.userByUsername(ctx, username)
	if err != nil {
		// Compare against a real hash so an unknown user costs the same time as a
		// known one (constant-time-ish; mitigates user enumeration).
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, errors.New("invalid credentials")
	}
	if !CheckPassword(u.PasswordHash, password) {
		return nil, errors.New("invalid credentials")
	}
	return u, nil
}

func (m *Manager) userByUsername(ctx context.Context, username string) (*models.User, error) {
	var u models.User
	err := m.pool.QueryRow(ctx,
		`SELECT id, username, email, password_hash, is_admin, role,
		        totp_secret, totp_enabled, created_at, updated_at
		 FROM users WHERE lower(username) = lower($1)`, username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.Role,
		&u.TOTPSecret, &u.TOTPEnabled, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateSession issues a new session and CSRF token for the given user and
// writes them as cookies on the response.
func (m *Manager) CreateSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := randomToken(32)
	if err != nil {
		return err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return err
	}
	expires := time.Now().Add(m.sessionMaxAge)
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	_, err = m.pool.Exec(ctx,
		`INSERT INTO sessions (token, user_id, expires_at, user_agent, ip, last_seen)
		 VALUES ($1, $2, $3, $4, $5, now())`,
		hashToken(token), userID, expires, ua, m.ClientIP(r))
	if err != nil {
		return err
	}

	m.writeSessionCookies(w, r, token, csrf, expires)
	return nil
}

// writeSessionCookies sets (or refreshes) the session and CSRF cookies so they
// expire at expires. Shared by session creation and sliding renewal.
func (m *Manager) writeSessionCookies(w http.ResponseWriter, r *http.Request, token, csrf string, expires time.Time) {
	secure := m.cookieSecure(r)
	maxAge := int(time.Until(expires).Seconds())
	// #nosec G124 -- HttpOnly + SameSite=Lax are set; Secure is derived from
	// cookieSecure(r) and is true behind TLS/a trusted proxy (false only for
	// plain-HTTP local dev).
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	// #nosec G124 -- the CSRF token is a double-submit value that JavaScript must
	// read to echo it back in a header, so HttpOnly is deliberately false.
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookie,
		Value:    csrf,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// slide extends a session's expiry (and cookie lifetime) on activity, capped by
// the absolute max age. No-op unless sliding is enabled, and only renews once
// less than half the idle window remains — limiting DB writes and Set-Cookie
// churn to roughly once per half-window.
func (m *Manager) slide(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if !m.sliding {
		return
	}
	sc, err := r.Cookie(SessionCookie)
	if err != nil || sc.Value == "" {
		return
	}
	cc, err := r.Cookie(CSRFCookie)
	if err != nil {
		return
	}
	tokenHash := hashToken(sc.Value)
	var createdAt, expiresAt time.Time
	if err := m.pool.QueryRow(ctx,
		`SELECT created_at, expires_at FROM sessions WHERE token=$1`, tokenHash).
		Scan(&createdAt, &expiresAt); err != nil {
		return
	}
	now := time.Now()
	if expiresAt.Sub(now) > m.sessionMaxAge/2 {
		return // still fresh; nothing to do
	}
	newExpiry := now.Add(m.sessionMaxAge)
	if capAt := createdAt.Add(m.absoluteMaxAge); newExpiry.After(capAt) {
		newExpiry = capAt
	}
	if !newExpiry.After(expiresAt) {
		return // at the absolute cap
	}
	if _, err := m.pool.Exec(ctx,
		`UPDATE sessions SET expires_at=$1 WHERE token=$2`, newExpiry, tokenHash); err != nil {
		return
	}
	m.writeSessionCookies(w, r, sc.Value, cc.Value, newExpiry)
}

// DestroySession removes the current session and clears cookies.
func (m *Manager) DestroySession(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		_, _ = m.pool.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, hashToken(c.Value))
	}
	m.clearCookie(w, SessionCookie)
	m.clearCookie(w, CSRFCookie)
}

func (m *Manager) clearCookie(w http.ResponseWriter, name string) {
	// #nosec G124 -- expiry cookie (empty value, MaxAge<0) that only deletes the
	// named cookie; attributes intentionally mirror how it was set.
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: name == SessionCookie,
		Secure:   m.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// userFromRequest looks up the user for the request's session cookie.
func (m *Manager) userFromRequest(ctx context.Context, r *http.Request) (*models.User, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil, errors.New("no session")
	}
	tokenHash := hashToken(c.Value)
	var u models.User
	var expires time.Time
	err = m.pool.QueryRow(ctx,
		`SELECT u.id, u.username, u.email, u.password_hash, u.is_admin, u.role,
		        u.totp_secret, u.totp_enabled, u.created_at, u.updated_at, s.expires_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token = $1`, tokenHash,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.Role,
		&u.TOTPSecret, &u.TOTPEnabled, &u.CreatedAt, &u.UpdatedAt, &expires)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("invalid session")
		}
		return nil, err
	}
	if time.Now().After(expires) {
		_, _ = m.pool.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, tokenHash)
		return nil, errors.New("session expired")
	}
	// Touch last_seen at most once a minute: the WHERE guard lets Postgres skip
	// the write entirely on a non-match (no dirty page / WAL), turning a
	// per-request write into an occasional one.
	_, _ = m.pool.Exec(ctx,
		`UPDATE sessions SET last_seen = now()
		 WHERE token = $1 AND last_seen < now() - interval '60 seconds'`, tokenHash)
	return &u, nil
}

// currentToken returns the hashed session token for the request (matching how
// tokens are stored), or "" if there is no session cookie.
func currentToken(r *http.Request) string {
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		return hashToken(c.Value)
	}
	return ""
}

// CleanupExpired deletes expired sessions; intended to run periodically.
func (m *Manager) CleanupExpired(ctx context.Context) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}

// UserFrom returns the authenticated user stored in the request context.
func UserFrom(ctx context.Context) (*models.User, bool) {
	u, ok := ctx.Value(userCtxKey).(*models.User)
	return u, ok
}

// RequireAuth is middleware that rejects unauthenticated API requests and
// enforces CSRF on state-changing methods.
func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := m.userFromRequest(r.Context(), r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		// Record who this request belongs to for the access log.
		setRequestLogUser(r.Context(), u.Username, u.ID)
		if isStateChanging(r.Method) && !m.csrfOK(r) {
			writeJSONError(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
		// Extend the session on activity when sliding expiration is enabled.
		m.slide(r.Context(), w, r)
		ctx := context.WithValue(r.Context(), userCtxKey, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin is middleware that additionally requires admin privileges.
func (m *Manager) RequireAdmin(next http.Handler) http.Handler {
	return m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		if u == nil || !u.IsAdmin {
			writeJSONError(w, http.StatusForbidden, "admin privileges required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// RequireRole is middleware that requires the user to hold one of the roles.
// Admins always pass.
func (m *Manager) RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, _ := UserFrom(r.Context())
			if u == nil || (!u.IsAdmin && !allowed[u.Role]) {
				writeJSONError(w, http.StatusForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

func (m *Manager) csrfOK(r *http.Request) bool {
	c, err := r.Cookie(CSRFCookie)
	if err != nil || c.Value == "" {
		return false
	}
	// Constant-time compare so the match doesn't leak via response timing.
	return subtle.ConstantTimeCompare([]byte(r.Header.Get(CSRFHeader)), []byte(c.Value)) == 1
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
