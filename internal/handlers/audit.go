package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/preining/parkrr/internal/models"
)

// ListAudit returns audit-log entries (admin only), newest first. Supports
// ?limit/?offset pagination plus optional filters: ?q (search username/summary),
// ?action and ?entity.
func (h *Handler) ListAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r, 50, 500)

	var where []string
	var args []any
	if q := trim(r.URL.Query().Get("q")); q != "" {
		args = append(args, "%"+q+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(username ILIKE $%d OR summary ILIKE $%d)", n, n))
	}
	if a := trim(r.URL.Query().Get("action")); a != "" {
		args = append(args, a)
		where = append(where, fmt.Sprintf("action = $%d", len(args)))
	}
	if e := trim(r.URL.Query().Get("entity")); e != "" {
		args = append(args, e)
		where = append(where, fmt.Sprintf("entity = $%d", len(args)))
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
