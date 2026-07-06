// Package handlers implements the JSON API and cost logic for Parkrr.
package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	Pool *pgxpool.Pool

	// CheckBreachedPasswords enables the HIBP k-anonymity check on new passwords.
	CheckBreachedPasswords bool
	hibpClient             *http.Client
}

// New constructs a Handler.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{
		Pool:       pool,
		hibpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// passwordBreached reports whether a new password appears in a known breach.
// It fails open: if the check is disabled or the HIBP API is unreachable, the
// password is allowed (availability of password changes is not held hostage to
// a third-party service), and the failure is logged.
func (h *Handler) passwordBreached(ctx context.Context, password string) bool {
	if !h.CheckBreachedPasswords {
		return false
	}
	n, err := auth.BreachedPasswordCount(ctx, h.hibpClient, password)
	if err != nil {
		slog.Warn("breached-password check unavailable, allowing", "err", err)
		return false
	}
	return n > 0
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// pathID extracts the positive int64 "id" path value.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func trim(s string) string { return strings.TrimSpace(s) }

// audit writes an entry to the audit log, deriving the acting user from the
// request context. Failures are ignored so auditing never breaks a request.
func (h *Handler) audit(r *http.Request, action, entity string, id int64, summary string) {
	var actorID int64
	var actorName string
	if u, ok := auth.UserFrom(r.Context()); ok {
		actorID = u.ID
		actorName = u.Username
	}
	h.auditAs(r, actorID, actorName, action, entity, id, summary)
}

// auditAs writes an audit entry with an explicit acting user. Use this where the
// user is not yet in the request context (e.g. at login).
func (h *Handler) auditAs(r *http.Request, actorID int64, actorName, action, entity string, id int64, summary string) {
	var uid *int64
	if actorID > 0 {
		uid = &actorID
	}
	var entID *int64
	if id > 0 {
		entID = &id
	}
	_, _ = h.Pool.Exec(r.Context(),
		`INSERT INTO audit_log (user_id, username, action, entity, entity_id, summary)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		uid, actorName, action, entity, entID, summary)
}
