// Package handlers implements the JSON API and cost logic for Parkrr.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
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
	// FailClosedOnBreach rejects a new password when the HIBP check can't run
	// (default false = fail open, allowing the change).
	FailClosedOnBreach bool
	hibpClient         *http.Client
}

// New constructs a Handler.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{
		Pool:       pool,
		hibpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// Input length policy (constants + valid*Length helpers) lives in validation.go.

// passwordBreached reports whether a new password appears in a known breach.
// If the check is disabled the password is allowed. If the HIBP API is
// unreachable the outcome follows FailClosedOnBreach: fail open by default (a
// change isn't held hostage to a third-party service) or reject when set to
// fail closed. The failure is logged either way.
func (h *Handler) passwordBreached(ctx context.Context, password string) bool {
	if !h.CheckBreachedPasswords {
		return false
	}
	n, err := auth.BreachedPasswordCount(ctx, h.hibpClient, password)
	if err != nil {
		if h.FailClosedOnBreach {
			slog.Warn("breached-password check unavailable, rejecting (fail-closed)", "err", err)
			return true
		}
		slog.Warn("breached-password check unavailable, allowing (fail-open)", "err", err)
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

var errNotJSON = errors.New("content type must be application/json")

func decodeJSON(r *http.Request, dst any) error {
	// Require a JSON content type when one is declared. A cross-site HTML form can
	// only send urlencoded/multipart/text-plain bodies, so rejecting those closes
	// CSRF on JSON endpoints (including login) without needing CORS. An absent type
	// is allowed — browsers always set one on cross-site form POSTs, and some
	// internal callers omit it.
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mt, _, _ := mime.ParseMediaType(ct); mt != "application/json" {
			return errNotJSON
		}
	}
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
