package handlers

import (
	"net/http"
	"time"
)

// mailLogEntry ist eine Zeile des Versandprotokolls (Hundert 86).
type mailLogEntry struct {
	ID         int64     `json:"id"`
	SentAt     time.Time `json:"sent_at"`
	Recipients string    `json:"recipients"`
	Subject    string    `json:"subject"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
}

// ListMailLog liefert die letzten Versandversuche, neueste zuerst (admin).
// "Hat der Kunde die Mahnung bekommen?" hat damit EINE Anlaufstelle statt
// dreier verstreuter (invoice_reminders, Audit, Server-Log).
func (h *Handler) ListMailLog(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r, 200, 1000)
	h.totalCount(w, r.Context(), `SELECT count(*) FROM mail_log`)
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, sent_at, recipients, subject, ok, error
		   FROM mail_log ORDER BY sent_at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []mailLogEntry{}
	for rows.Next() {
		var e mailLogEntry
		if err := rows.Scan(&e.ID, &e.SentAt, &e.Recipients, &e.Subject, &e.OK, &e.Error); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
