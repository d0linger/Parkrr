package handlers

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"time"
)

// Upload limits.
const (
	maxPhotoBytes       = 8 << 20 // 8 MiB raw upload cap
	maxPhotoDimension   = 8000    // px, guards against decompression bombs
	maxPhotosPerVehicle = 20
	jpegQuality         = 85
)

type photoMeta struct {
	ID          int64     `json:"id"`
	VehicleID   int64     `json:"vehicle_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	ByteSize    int       `json:"byte_size"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListPhotos returns photo metadata for a vehicle (not the image bytes).
func (h *Handler) ListPhotos(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, vehicle_id, filename, content_type, byte_size, created_at
		 FROM vehicle_photos WHERE vehicle_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []photoMeta{}
	for rows.Next() {
		var p photoMeta
		if err := rows.Scan(&p.ID, &p.VehicleID, &p.Filename, &p.ContentType,
			&p.ByteSize, &p.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

// UploadPhoto accepts a multipart image upload for a vehicle. The image is
// decoded and re-encoded to strip any metadata (EXIF/GPS) and reject files that
// are not genuine JPEG/PNG images.
func (h *Handler) UploadPhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var count int
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM vehicle_photos WHERE vehicle_id=$1`, id).Scan(&count); err != nil {
		writeError(w, http.StatusNotFound, "vehicle not found")
		return
	}
	if count >= maxPhotosPerVehicle {
		writeError(w, http.StatusConflict, "photo limit reached for this vehicle")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes+1024)
	// #nosec G120 -- the request body is capped by MaxBytesReader above, so the
	// multipart parse is bounded and cannot exhaust memory.
	if err := r.ParseMultipartForm(maxPhotoBytes + 1024); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'photo' file field")
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
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}

	filename := trim(header.Filename)
	if len(filename) > 200 {
		filename = filename[:200]
	}
	var photoID int64
	if err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO vehicle_photos (vehicle_id, filename, content_type, byte_size, data)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		id, filename, contentType, len(data), data).Scan(&photoID); err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not store photo")
		return
	}
	h.audit(r, "create", "photo", photoID, "uploaded photo for vehicle")
	writeJSON(w, http.StatusCreated, photoMeta{
		ID: photoID, VehicleID: id, Filename: filename,
		ContentType: contentType, ByteSize: len(data), CreatedAt: time.Now(),
	})
}

// sanitizeImage validates dimensions, decodes and re-encodes the image to strip
// metadata. Returns the clean bytes and their content type.
func sanitizeImage(raw []byte) ([]byte, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, "", errImage("could not read image")
	}
	if format != "jpeg" && format != "png" {
		return nil, "", errImage("only JPEG or PNG images are allowed")
	}
	if cfg.Width > maxPhotoDimension || cfg.Height > maxPhotoDimension ||
		cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", errImage("image dimensions out of range")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", errImage("could not decode image")
	}
	var buf bytes.Buffer
	if format == "png" {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", errImage("could not process image")
		}
		return buf.Bytes(), "image/png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", errImage("could not process image")
	}
	return buf.Bytes(), "image/jpeg", nil
}

type imageError struct{ msg string }

func (e imageError) Error() string { return e.msg }
func errImage(msg string) error    { return imageError{msg} }

// GetPhoto streams the raw image bytes for a photo.
func (h *Handler) GetPhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var ct string
	var data []byte
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT content_type, data FROM vehicle_photos WHERE id=$1`, id).Scan(&ct, &data); err != nil {
		writeError(w, http.StatusNotFound, "photo not found")
		return
	}
	// Never trust the stored type on output — serve only image types the uploader
	// is allowed to produce, so a future bad row can't be rendered as inline HTML.
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

// DeletePhoto removes a photo.
func (h *Handler) DeletePhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM vehicle_photos WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete photo")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "photo not found")
		return
	}
	h.audit(r, "delete", "photo", id, "deleted photo")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
