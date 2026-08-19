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

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/backup"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/mail"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	Pool *pgxpool.Pool

	// CheckBreachedPasswords enables the HIBP k-anonymity check on new passwords.
	CheckBreachedPasswords bool
	// FailClosedOnBreach rejects a new password when the HIBP check can't run
	// (default false = fail open, allowing the change).
	FailClosedOnBreach bool
	// BackupKey (if set) enables the encrypted-backup endpoint; DatabaseURL is
	// the connection string handed to pg_dump; BackupDir holds scheduled backups.
	BackupKey   string
	DatabaseURL string
	BackupDir   string
	S3          backup.S3Config
	hibpClient  *http.Client
	// Mail sends transactional e-mail (payment reminders). Defaults to a disabled
	// sender when SMTP is not configured, so it is never nil.
	Mail mail.Sender
	// PublicBaseURL is the externally reachable base (e.g. https://parkrr.example.com),
	// used to build links in outgoing e-mail. Empty falls back to a relative hint.
	PublicBaseURL string
	// Auth is the session manager, used here only for RequestIsHTTPS so scheme
	// detection (e.g. in the QR label) honors the trusted-proxy CIDR gate. May be
	// nil in tests that construct a bare Handler.
	Auth *auth.Manager
}

// New constructs a Handler.
func New(pool *pgxpool.Pool) *Handler {
	return &Handler{
		Pool:       pool,
		hibpClient: &http.Client{Timeout: 5 * time.Second},
		Mail:       mail.New(mail.Config{}), // disabled until configured
	}
}

// Input length policy (constants + valid*Length helpers) lives in validation.go.

// breachResult is the outcome of a new-password breach check.
type breachResult int

const (
	breachOK          breachResult = iota // safe (or the check is disabled / failed open)
	breachFound                           // the password appears in a known breach
	breachUnavailable                     // the check couldn't run and policy is fail-closed
)

// checkPasswordBreach classifies a new password. If the check is disabled the
// password is allowed. If the HIBP API is unreachable the outcome follows
// FailClosedOnBreach: fail open by default (a change isn't held hostage to a
// third-party service) or report "unavailable" when set to fail closed. The
// failure is logged either way.
func (h *Handler) checkPasswordBreach(ctx context.Context, password string) breachResult {
	if !h.CheckBreachedPasswords {
		return breachOK
	}
	n, err := auth.BreachedPasswordCount(ctx, h.hibpClient, password)
	if err != nil {
		if h.FailClosedOnBreach {
			slog.Warn("breached-password check unavailable, rejecting (fail-closed)", "err", err)
			return breachUnavailable
		}
		slog.Warn("breached-password check unavailable, allowing (fail-open)", "err", err)
		return breachOK
	}
	if n > 0 {
		return breachFound
	}
	return breachOK
}

// rejectBreachedPassword runs the breach check and, if the password must be
// rejected, writes the appropriate response and returns true (so the caller
// returns). A confirmed breach is a 400 with a clear message; an unavailable
// check under a fail-closed policy is a 503 (try again) — never mislabelled as
// a known breach.
func (h *Handler) rejectBreachedPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	switch h.checkPasswordBreach(r.Context(), password) {
	case breachFound:
		writeError(w, http.StatusBadRequest, "this password has appeared in a known data breach; please choose another")
		return true
	case breachUnavailable:
		writeError(w, http.StatusServiceUnavailable, "could not verify the password against the breach database; please try again shortly")
		return true
	default:
		return false
	}
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
		if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/json" {
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

// execer is the subset of *pgxpool.Pool and pgx.Tx used to write an audit row,
// so the row can be inserted either standalone (post-commit, best-effort) or
// inside a money transaction (atomic with the mutation).
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// actorFrom derives the acting user (id, name) from the request context, or
// (0, "") when none is present (e.g. before login).
func actorFrom(r *http.Request) (int64, string) {
	if u, ok := auth.UserFrom(r.Context()); ok {
		return u.ID, u.Username
	}
	return 0, ""
}

// audit writes an entry to the audit log, deriving the acting user from the
// request context. Failures are ignored so auditing never breaks a request.
//
// This is the post-commit, best-effort path (separate connection). For money
// mutations use auditChangeTx inside the transaction so the trail is atomic with
// the change (BAO §131) — a crash can never leave a booked change without its
// audit row.
func (h *Handler) audit(r *http.Request, action, entity string, id int64, summary string) {
	h.auditChange(r, action, entity, id, summary, nil)
}

// auditChangeTx writes an audit entry through q — pass the money transaction's
// pgx.Tx so the row commits atomically with the mutation and rolls back with it.
// The error is returned so the caller fails (and rolls back) the tx on an audit
// write failure: a booked money change must not persist without its trail.
// Records per-field before/after values (pass the result of diffFields); a
// nil/empty changes is stored as NULL.
func (h *Handler) auditChangeTx(ctx context.Context, q execer, r *http.Request, action, entity string, id int64, summary string, changes any) error {
	actorID, actorName := actorFrom(r)
	return auditExec(ctx, q, actorID, actorName, action, entity, id, summary, changes)
}

// auditChange is like audit but also records per-field before/after values
// (pass the result of diffFields for updates). A nil/empty changes is stored as
// NULL.
func (h *Handler) auditChange(r *http.Request, action, entity string, id int64, summary string, changes any) {
	actorID, actorName := actorFrom(r)
	h.auditInsert(r, actorID, actorName, action, entity, id, summary, changes)
}

// strPtr renders a *string for an audit diff: nil stays nil so "not set" remains
// distinguishable from an empty value.
func strPtr(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

// auditDate renders an optional date for an audit diff. Like strPtr, nil stays nil
// so "no end date" stays distinguishable from a date that happens to be empty.
func auditDate(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02")
}

// blobState describes a binary column for an audit diff without reading it into
// the trail. The bytes themselves (a floor plan, a geometry blob) are noise in a
// log and can be large; what an auditor needs is whether it was set and whether it
// changed, which the size captures.
func blobState(b []byte) string {
	if len(b) == 0 {
		return "leer"
	}
	return "gesetzt (" + strconv.Itoa(len(b)) + " B)"
}

// auditSnapshot renders values as {field: {old: null, new: v}} — "this is what it
// became", with no claim about what it was before.
//
// Use it wherever the previous value is genuinely unknown, or where the entry is a
// COUNT or an outcome rather than a field transition (how many positions were
// settled, which period was booked). Inventing an old value there is worse than
// omitting one: a fabricated `false -> true` asserts a transition that may never
// have happened, and diffFields would silently drop any key whose sides are equal,
// losing exactly the identifying field the entry exists for.
func auditSnapshot(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = map[string]any{"old": nil, "new": v}
	}
	return out
}

// AuditValues is the exported form of auditSnapshot, for the background jobs that
// live in other packages and record an outcome rather than a before/after.
func AuditValues(values map[string]any) map[string]any { return auditSnapshot(values) }

// mergeChanges combines change maps (e.g. a real diff plus an auditSnapshot of the
// outcome fields) into one changes object. Later maps win on a key collision.
func mergeChanges(maps ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// auditCreated records a creation together with the values the new row was given,
// as {field: {old: null, new: value}} — the mirror image of auditDeleted. Without
// it a "created X" entry proves only THAT something appeared, never with which
// values, so a later edit cannot be told apart from the original state.
//
// Note for both helpers: events that are not data changes (login, backup, restore,
// reminders, imports) deliberately keep a plain summary — they have no fields to
// diff, and inventing one would only add noise to the trail.
func (h *Handler) auditCreated(r *http.Request, entity string, id int64, summary string, snapshot map[string]any) {
	// auditSnapshot already produces exactly {field: {old: nil, new: v}} and returns nil
	// for an empty input, which is what a creation needs. (auditDeleted cannot share it:
	// its map is the mirror image, {old: v, new: nil}.)
	h.auditChange(r, "create", entity, id, summary, auditSnapshot(snapshot))
}

// auditDeleted records a deletion together with the identifying values of the row
// that was removed. This matters more than any other audit case: once the row is
// gone, an entry that carries only an id can never be resolved back to WHAT was
// deleted. Callers get the snapshot from `DELETE … RETURNING`, so it costs no extra
// query and is atomic with the delete. Values still pass the redaction choke point.
func (h *Handler) auditDeleted(r *http.Request, entity string, id int64, summary string, snapshot map[string]any) {
	var changes map[string]any
	if len(snapshot) > 0 {
		changes = make(map[string]any, len(snapshot))
		for k, v := range snapshot {
			changes[k] = map[string]any{"old": v, "new": nil} // deleted: old value, no new one
		}
	}
	h.auditChange(r, "delete", entity, id, summary, changes)
}

// auditAs writes an audit entry with an explicit acting user. Use this where the
// user is not yet in the request context (e.g. at login).
func (h *Handler) auditAs(r *http.Request, actorID int64, actorName, action, entity string, id int64, summary string) {
	h.auditInsert(r, actorID, actorName, action, entity, id, summary, nil)
}

func (h *Handler) auditInsert(r *http.Request, actorID int64, actorName, action, entity string, id int64, summary string, changes any) {
	// Best-effort, post-commit: never break a request on an audit failure.
	_ = auditExec(r.Context(), h.Pool, actorID, actorName, action, entity, id, summary, changes)
}

// AuditSystem records an action taken outside a request handler: a scheduled backup,
// the retention sweep, the archival job. It routes through auditExec like every other
// path, so the redaction choke point still applies and a background job cannot leak a
// secret into the trail either.
//
// The actor comes from ctx when one is there. Several of these paths are reachable
// BOTH from a background sweep and from a request (archival runs on every settlement
// toggle), and hard-coding "system" attributed a user's own action to nobody — the
// destructive half of an operator's click losing its attribution is exactly what an
// audit trail must not do. Only a genuinely request-less caller records "system".
//
// changes is taken as-is, like auditChange and auditChangeTx: a caller with a real
// before/after passes diffFields, a caller with only an outcome passes auditSnapshot.
// Wrapping it here would have silently double-nested any future diffFields caller.
//
// Exported because the schedulers live outside this package (internal/backup cannot
// import handlers — handlers already imports it), so main injects this as a callback.
// Best-effort: a failed audit write must never abort a backup or a sweep.
func (h *Handler) AuditSystem(ctx context.Context, action, entity string, id int64, summary string, changes any) {
	var actorID int64
	actorName := "system"
	if u, ok := auth.UserFrom(ctx); ok {
		actorID, actorName = u.ID, u.Username
	}
	if err := auditExec(ctx, h.Pool, actorID, actorName, action, entity, id, summary, changes); err != nil {
		slog.Warn("audit: system entry failed", "action", action, "entity", entity, "err", err)
	}
}

// auditExec inserts one audit row through q (pool or tx) and returns the write
// error. It is the single INSERT shared by the best-effort and transactional
// paths, so the row shape stays identical either way.
func auditExec(ctx context.Context, q execer, actorID int64, actorName, action, entity string, id int64, summary string, changes any) error {
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
		// Redact BEFORE marshaling — this is the single choke point every audit
		// write passes through, so a secret cannot reach the trail even if a call
		// site forgets to exclude the field (see audit_redact.go).
		if b, err := json.Marshal(redactChanges(normalizeChanges(changes))); err == nil && string(b) != "null" {
			changesArg = string(b)
		}
	}
	_, err := q.Exec(ctx,
		`INSERT INTO audit_log (user_id, username, action, entity, entity_id, summary, changes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		uid, actorName, action, entity, entID, summary, changesArg)
	return err
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
