// Package handlers implements the JSON API and cost logic for Parkrr.
package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	Pool *pgxpool.Pool
}

// New constructs a Handler.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{Pool: pool}
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
