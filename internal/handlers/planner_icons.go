package handlers

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Custom Garagenplaner icons ("tags"): user-uploaded top-view images selectable per
// vehicle. Uploads reuse sanitizeImage (decode + re-encode PNG/JPEG, strip metadata),
// so stored bytes are always a genuine, script-free raster image.
const maxPlannerIcons = 60

// plannerIconLock serializes the upload quota check + insert via a transaction-scoped
// advisory lock, so concurrent uploads cannot both pass the count and exceed the cap.
const plannerIconLock int64 = 0x504C4E52 // "PLNR"

type plannerIconMeta struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	ByteSize    int       `json:"byte_size"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListPlannerIcons returns icon metadata (not the image bytes).
func (h *Handler) ListPlannerIcons(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, name, content_type, byte_size, created_at
		 FROM planner_icons ORDER BY lower(name)`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []plannerIconMeta{}
	for rows.Next() {
		var p plannerIconMeta
		if err := rows.Scan(&p.ID, &p.Name, &p.ContentType, &p.ByteSize, &p.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// UploadPlannerIcon accepts a multipart image ("file") plus a "name" tag. The image is
// decoded and re-encoded to strip metadata and reject non-JPEG/PNG uploads.
func (h *Handler) UploadPlannerIcon(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes+1024)
	// #nosec G120 -- the request body is capped by MaxBytesReader above.
	if err := r.ParseMultipartForm(maxPhotoBytes + 1024); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }() // drop any spilled temp files
	name := trim(r.FormValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validNameLength(name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, maxPhotoBytes+1))
	if err != nil || len(raw) > maxPhotoBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	data, contentType, err := sanitizeImage(raw)
	if err != nil {
		if errors.Is(err, errDecodeBusy) {
			writeError(w, http.StatusServiceUnavailable, "server busy, please retry shortly")
			return
		}
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}

	// Serialize the quota check + insert with a transaction-scoped advisory lock so
	// concurrent uploads cannot both observe a count below the cap and then exceed it.
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not store icon")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock($1)`, plannerIconLock); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store icon")
		return
	}
	var count int
	if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM planner_icons`).Scan(&count); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if count >= maxPlannerIcons {
		writeError(w, http.StatusConflict, "icon limit reached")
		return
	}
	var iconID int64
	var createdAt time.Time
	if err := tx.QueryRow(r.Context(),
		`INSERT INTO planner_icons (name, content_type, byte_size, data)
		 VALUES ($1,$2,$3,$4) RETURNING id, created_at`,
		name, contentType, len(data), data).Scan(&iconID, &createdAt); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "an icon with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not store icon")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store icon")
		return
	}
	h.audit(r, "create", "planner_icon", iconID, "uploaded planner icon "+name)
	writeJSON(w, http.StatusCreated, plannerIconMeta{
		ID: iconID, Name: name, ContentType: contentType, ByteSize: len(data), CreatedAt: createdAt,
	})
}

// UpdatePlannerIcon renames a custom icon and/or replaces its image, KEEPING the same
// id so existing vehicle overrides (planner_symbol='custom:<id>') stay valid. Multipart:
// optional "name", optional "file" (at least one must be present).
func (h *Handler) UpdatePlannerIcon(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes+1024)
	// #nosec G120 -- the request body is capped by MaxBytesReader above.
	if err := r.ParseMultipartForm(maxPhotoBytes + 1024); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }() // drop any spilled temp files
	name := trim(r.FormValue("name"))
	if name != "" && !validNameLength(name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	var data []byte
	var contentType string
	// A missing "file" field is fine (rename-only); any other FormFile error is a real
	// client error and must not be silently treated as "no image".
	file, _, ferr := r.FormFile("file")
	if ferr != nil && !errors.Is(ferr, http.ErrMissingFile) {
		writeError(w, http.StatusBadRequest, "invalid 'file' field")
		return
	}
	if ferr == nil {
		defer file.Close()
		raw, rerr := io.ReadAll(io.LimitReader(file, maxPhotoBytes+1))
		if rerr != nil || len(raw) > maxPhotoBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large")
			return
		}
		d, ct, serr := sanitizeImage(raw)
		if serr != nil {
			if errors.Is(serr, errDecodeBusy) {
				writeError(w, http.StatusServiceUnavailable, "server busy, please retry shortly")
				return
			}
			writeError(w, http.StatusUnsupportedMediaType, serr.Error())
			return
		}
		data, contentType = d, ct
	}
	if name == "" && data == nil {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	var query string
	var args []any
	switch {
	case name != "" && data != nil:
		query = `UPDATE planner_icons SET name=$1, content_type=$2, byte_size=$3, data=$4 WHERE id=$5`
		args = []any{name, contentType, len(data), data, id}
	case name != "":
		query = `UPDATE planner_icons SET name=$1 WHERE id=$2`
		args = []any{name, id}
	default:
		query = `UPDATE planner_icons SET content_type=$1, byte_size=$2, data=$3 WHERE id=$4`
		args = []any{contentType, len(data), data, id}
	}
	ct, err := h.Pool.Exec(r.Context(), query, args...)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "an icon with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update icon")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "icon not found")
		return
	}
	h.audit(r, "update", "planner_icon", id, "updated planner icon")
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// GetPlannerIcon streams the raw image bytes for an icon.
func (h *Handler) GetPlannerIcon(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var ct string
	var data []byte
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT content_type, data FROM planner_icons WHERE id=$1`, id).Scan(&ct, &data); err != nil {
		writeError(w, http.StatusNotFound, "icon not found")
		return
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct != "image/jpeg" && ct != "image/png" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

// DeletePlannerIcon removes a custom icon. Vehicles still referencing it fall back to
// the built-in category symbol on the client (broken-image onerror), so no cascade.
func (h *Handler) DeletePlannerIcon(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	// Clear any vehicle overrides pointing at this icon in the SAME transaction, so a
	// deleted icon never leaves a dangling 'custom:<id>' that a later full vehicle
	// update would reject (400 from normPlannerSymbol).
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete icon")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err := tx.Exec(r.Context(),
		`UPDATE vehicles SET planner_symbol=NULL, updated_at=now() WHERE planner_symbol=$1`,
		"custom:"+strconv.FormatInt(id, 10)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete icon")
		return
	}
	ct, err := tx.Exec(r.Context(), `DELETE FROM planner_icons WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete icon")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "icon not found")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete icon")
		return
	}
	h.audit(r, "delete", "planner_icon", id, "deleted planner icon")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
