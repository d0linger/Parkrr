package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/preining/parkrr/internal/models"
)

// ListSessions returns the active sessions for a user, marking the current one.
func (m *Manager) ListSessions(ctx context.Context, userID int64, currentTok string) ([]models.SessionInfo, error) {
	rows, err := m.pool.Query(ctx,
		`SELECT token, user_agent, ip, last_seen, created_at, expires_at
		 FROM sessions WHERE user_id = $1 AND expires_at > now()
		 ORDER BY last_seen DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []models.SessionInfo{}
	for rows.Next() {
		var s models.SessionInfo
		if err := rows.Scan(&s.Token, &s.UserAgent, &s.IP, &s.LastSeen,
			&s.CreatedAt, &s.ExpiresAt); err != nil {
			return nil, err
		}
		s.Current = s.Token == currentTok
		// Never expose the full token to the client; keep a short handle.
		if len(s.Token) > 8 {
			s.Token = s.Token[:8]
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RevokeSession deletes a single session owned by the user, identified by a
// token prefix (the handle returned by ListSessions).
func (m *Manager) RevokeSession(ctx context.Context, userID int64, handle string) (int64, error) {
	ct, err := m.pool.Exec(ctx,
		`DELETE FROM sessions WHERE user_id = $1 AND left(token, 8) = $2`, userID, handle)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// RevokeOtherSessions deletes all of the user's sessions except the current one.
func (m *Manager) RevokeOtherSessions(ctx context.Context, userID int64, keepToken string) error {
	_, err := m.pool.Exec(ctx,
		`DELETE FROM sessions WHERE user_id = $1 AND token <> $2`, userID, keepToken)
	return err
}

// RevokeAllSessions deletes every session for a user, signing them out on all
// devices. Use after a password change or an admin-forced credential reset.
func (m *Manager) RevokeAllSessions(ctx context.Context, userID int64) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

// RotateSession invalidates all of the user's existing sessions and issues a
// fresh session + CSRF token on the response. Use it right after a password
// change so the new credentials are bound to a brand-new session and any other
// (potentially compromised) sessions are terminated.
func (m *Manager) RotateSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID int64) error {
	verified := false
	if u, ok := UserFrom(ctx); ok && u != nil && u.ID == userID {
		verified = u.FactorVerified
	}
	if err := m.RevokeAllSessions(ctx, userID); err != nil {
		return err
	}
	return m.createSession(ctx, w, r, userID, verified)
}

// RotateSessionTx stages revocation and replacement in the caller's
// transaction, returning a cookie writer that must be invoked only after a
// successful commit. This lets credential updates and session invalidation form
// one atomic state transition without issuing cookies for a rolled-back row.
func (m *Manager) RotateSessionTx(ctx context.Context, tx pgx.Tx, r *http.Request, userID int64) (func(http.ResponseWriter), error) {
	verified := false
	if u, ok := UserFrom(ctx); ok && u != nil && u.ID == userID {
		verified = u.FactorVerified
	}
	token, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf := m.csrfToken(token)
	expires := time.Now().Add(m.sessionMaxAge)
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sessions (token, user_id, expires_at, user_agent, ip, last_seen, factor_verified)
		 VALUES ($1,$2,$3,$4,$5,now(),$6)`,
		hashToken(token), userID, expires, ua, m.ClientIP(r), verified); err != nil {
		return nil, err
	}
	return func(w http.ResponseWriter) { m.writeSessionCookies(w, r, token, csrf, expires) }, nil
}

// CurrentToken exposes the request's session token (used by handlers).
func CurrentToken(r *http.Request) string { return currentToken(r) }
