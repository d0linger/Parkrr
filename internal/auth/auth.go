// Package auth handles password hashing, session management and middleware.
package auth

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
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
	pool          *pgxpool.Pool
	sessionMaxAge time.Duration
	secureCookies bool
	trustProxy    bool
	aead          cipher.AEAD
}

// NewManager constructs an auth Manager. secret is used to derive the key that
// encrypts TOTP secrets at rest.
func NewManager(pool *pgxpool.Pool, sessionMaxAge int, secureCookies, trustProxy bool, secret string) (*Manager, error) {
	aead, err := newAEAD(secret)
	if err != nil {
		return nil, err
	}
	return &Manager{
		pool:          pool,
		sessionMaxAge: time.Duration(sessionMaxAge) * time.Second,
		secureCookies: secureCookies,
		trustProxy:    trustProxy,
		aead:          aead,
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
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
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
		// Run a dummy hash to reduce user-enumeration timing differences.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinva"), []byte(password))
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

	secure := m.cookieSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(m.sessionMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	// CSRF token is readable by JS so it can be echoed back in a header.
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookie,
		Value:    csrf,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(m.sessionMaxAge.Seconds()),
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
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
	_, _ = m.pool.Exec(ctx, `UPDATE sessions SET last_seen = now() WHERE token = $1`, tokenHash)
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
		if isStateChanging(r.Method) && !m.csrfOK(r) {
			writeJSONError(w, http.StatusForbidden, "invalid CSRF token")
			return
		}
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
	return r.Header.Get(CSRFHeader) == c.Value
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
