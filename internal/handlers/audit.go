package handlers

import (
	"net/http"
	"strconv"

	"github.com/preining/parkrr/internal/models"
)

// ListAudit returns recent audit-log entries (admin only). Supports ?limit=.
func (h *Handler) ListAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, user_id, username, action, entity, entity_id, summary, created_at
		 FROM audit_log ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []models.AuditEntry{}
	for rows.Next() {
		var a models.AuditEntry
		if err := rows.Scan(&a.ID, &a.UserID, &a.Username, &a.Action, &a.Entity,
			&a.EntityID, &a.Summary, &a.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, out)
}
