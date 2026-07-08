package handlers

import "net/http"

// personLabel returns a person's display name for audit messages ("" on error).
// Flat rates are now modelled as agreement records — see agreements.go.
func (h *Handler) personLabel(r *http.Request, id int64) string {
	var name string
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT trim(first_name || ' ' || last_name) FROM persons WHERE id=$1`, id).Scan(&name)
	return name
}
