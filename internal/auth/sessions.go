package auth

import (
	"context"
	"net/http"

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

// CurrentToken exposes the request's session token (used by handlers).
func CurrentToken(r *http.Request) string { return currentToken(r) }
