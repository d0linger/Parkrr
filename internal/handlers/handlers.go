// Package handlers implements the JSON API and cost logic for Parkrr.
package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
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

// Password length policy: bcrypt hashes only the first 72 bytes and x/crypto
// rejects longer inputs outright (a 500 without this check), so cap what we
// accept up front. len() counts bytes, matching bcrypt's limit.
const (
	maxUsernameLen = 100
	minPasswordLen = 8
	maxPasswordLen = 72
)

// validUsernameLength reports whether s is a non-empty username within the
// length cap. Bounding it protects the rate limiter (which keys on the
// username) from memory pressure via over-long keys.
func validUsernameLength(s string) bool {
	return len(s) > 0 && len(s) <= maxUsernameLen
}

// validPasswordLength reports whether pw satisfies the length policy.
func validPasswordLength(pw string) bool {
	return len(pw) >= minPasswordLen && len(pw) <= maxPasswordLen
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

// pageParams parses ?limit and ?offset for list endpoints, applying a default
// page size and a hard maximum so a single request can never pull an unbounded
// result set into memory.
func pageParams(r *http.Request, defLimit, maxLimit int) (limit, offset int) {
	limit = defLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n > 0 {
			offset = n
		}
	}
	return limit, offset
}

// audit writes an entry to the audit log, deriving the acting user from the
// request context. Failures are ignored so auditing never breaks a request.
func (h *Handler) audit(r *http.Request, action, entity string, id int64, summary string) {
	h.auditChange(r, action, entity, id, summary, nil)
}

// auditChange is like audit but also records per-field before/after values
// (pass the result of diffFields for updates). A nil/empty changes is stored as
// NULL.
func (h *Handler) auditChange(r *http.Request, action, entity string, id int64, summary string, changes any) {
	var actorID int64
	var actorName string
	if u, ok := auth.UserFrom(r.Context()); ok {
		actorID = u.ID
		actorName = u.Username
	}
	h.auditInsert(r, actorID, actorName, action, entity, id, summary, changes)
}

// auditAs writes an audit entry with an explicit acting user. Use this where the
// user is not yet in the request context (e.g. at login).
func (h *Handler) auditAs(r *http.Request, actorID int64, actorName, action, entity string, id int64, summary string) {
	h.auditInsert(r, actorID, actorName, action, entity, id, summary, nil)
}

func (h *Handler) auditInsert(r *http.Request, actorID int64, actorName, action, entity string, id int64, summary string, changes any) {
	var uid *int64
	if actorID > 0 {
		uid = &actorID
	}
	var entID *int64
	if id > 0 {
		entID = &id
	}
	var changesArg any // nil -> SQL NULL
	if changes != nil {
		if b, err := json.Marshal(changes); err == nil && string(b) != "null" {
			changesArg = string(b)
		}
	}
	_, _ = h.Pool.Exec(r.Context(),
		`INSERT INTO audit_log (user_id, username, action, entity, entity_id, summary, changes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		uid, actorName, action, entity, entID, summary, changesArg)
}

// diffFields compares the JSON representations of old and new and returns the
// changed fields as {"field": {"old": x, "new": y}}, or nil if nothing changed.
// Fields named in skip (e.g. timestamps) are ignored.
func diffFields(oldObj, newObj any, skip ...string) map[string]any {
	om, nm := toJSONMap(oldObj), toJSONMap(newObj)
	skipset := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipset[s] = true
	}
	out := map[string]any{}
	for k, nv := range nm {
		if skipset[k] {
			continue
		}
		if ov, ok := om[k]; !ok || !reflect.DeepEqual(ov, nv) {
			out[k] = map[string]any{"old": om[k], "new": nv}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func toJSONMap(v any) map[string]any {
	m := map[string]any{}
	if v == nil {
		return m
	}
	if b, err := json.Marshal(v); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}
