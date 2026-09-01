package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/preining/parkrr/internal/models"
)

// ListAudit returns audit-log entries (admin only), newest first. Supports
// ?limit/?offset pagination plus optional filters: ?q (search username/summary),
// ?action, ?entity and a ?from/?to date range (YYYY-MM-DD, "to" inclusive).
func (h *Handler) ListAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r, 50, 500)

	var where []string
	var args []any
	if q := trim(r.URL.Query().Get("q")); q != "" {
		if !validNameLength(q) {
			writeError(w, http.StatusBadRequest, "query parameter is too long")
			return
		}
		args = append(args, "%"+q+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(username ILIKE $%d OR summary ILIKE $%d)", n, n))
	}
	if a := trim(r.URL.Query().Get("action")); a != "" {
		if !validNameLength(a) {
			writeError(w, http.StatusBadRequest, "action is too long")
			return
		}
		args = append(args, a)
		where = append(where, fmt.Sprintf("action = $%d", len(args)))
	}
	if e := trim(r.URL.Query().Get("entity")); e != "" {
		if !validNameLength(e) {
			writeError(w, http.StatusBadRequest, "entity is too long")
			return
		}
		args = append(args, e)
		where = append(where, fmt.Sprintf("entity = $%d", len(args)))
	}
	// Optional date range (YYYY-MM-DD). Parsed in the server's local zone (not UTC)
	// so a picked day matches the operator's calendar day at its boundaries; a
	// malformed value is simply ignored (no filter) rather than reaching the query.
	if f := trim(r.URL.Query().Get("from")); f != "" {
		if !validDateLength(f) {
			writeError(w, http.StatusBadRequest, "from date is too long")
			return
		}
		if d, perr := time.ParseInLocation("2006-01-02", f, time.Local); perr == nil {
			args = append(args, d)
			where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
		}
	}
	if to := trim(r.URL.Query().Get("to")); to != "" {
		if !validDateLength(to) {
			writeError(w, http.StatusBadRequest, "to date is too long")
			return
		}
		if d, perr := time.ParseInLocation("2006-01-02", to, time.Local); perr == nil {
			args = append(args, d.AddDate(0, 0, 1)) // inclusive of the whole "to" day
			where = append(where, fmt.Sprintf("created_at < $%d", len(args)))
		}
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit, offset)
	query := `SELECT id, user_id, username, action, entity, entity_id, summary, changes, created_at
	          FROM audit_log` + clause +
		fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := h.Pool.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []models.AuditEntry{}
	for rows.Next() {
		var a models.AuditEntry
		if err := rows.Scan(&a.ID, &a.UserID, &a.Username, &a.Action, &a.Entity,
			&a.EntityID, &a.Summary, &a.Changes, &a.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
