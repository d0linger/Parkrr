package handlers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Datei-Anhänge je Person und Gefährt (Hundert 57). Erlaubt sind PDF und die
// beiden Bildformate der Foto-Pipeline. Bilder laufen durch sanitizeImage
// (dekodieren, neu kodieren, Metadaten weg); PDFs lassen sich nicht neu kodieren
// — dort prüft die Magic-Number, und die Auslieferung erzwingt Download
// (Content-Disposition: attachment) plus nosniff: ein PDF kann Skripte tragen,
// und als erzwungener Download läuft keines davon im Kontext der Anwendung.
const (
	maxAttachmentBytes     = 10 << 20 // 10 MiB je Datei
	maxAttachmentsPerOwner = 20
)

type attachmentMeta struct {
	ID          int64     `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	ByteSize    int       `json:"byte_size"`
	CreatedAt   time.Time `json:"created_at"`
}

// attachmentOwner löst {id} + Route in die Besitzerspalte auf.
func attachmentOwner(r *http.Request) (col string, id int64, ok bool) {
	id, ok = pathID(r)
	if !ok {
		return "", 0, false
	}
	if strings.Contains(r.URL.Path, "/vehicles/") {
		return "vehicle_id", id, true
	}
	return "person_id", id, true
}

// ListAttachments liefert die Metadaten (nie die Bytes) eines Besitzers.
func (h *Handler) ListAttachments(w http.ResponseWriter, r *http.Request) {
	col, id, ok := attachmentOwner(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, filename, content_type, byte_size, created_at
		   FROM attachments WHERE `+col+`=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []attachmentMeta{}
	for rows.Next() {
		var a attachmentMeta
		if err := rows.Scan(&a.ID, &a.Filename, &a.ContentType, &a.ByteSize, &a.CreatedAt); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// UploadAttachment nimmt eine Datei an (multipart "file"), prüft sie nach Typ
// und speichert sie beim Besitzer.
func (h *Handler) UploadAttachment(w http.ResponseWriter, r *http.Request) {
	col, id, ok := attachmentOwner(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var count int
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM attachments WHERE `+col+`=$1`, id).Scan(&count); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if count >= maxAttachmentsPerOwner {
		writeError(w, http.StatusConflict, "Anhang-Limit erreicht")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBytes+1024)
	// #nosec G120 -- der Body ist durch MaxBytesReader gedeckelt.
	if err := r.ParseMultipartForm(maxAttachmentBytes + 1024); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "Datei ist zu groß (max. 10 MB)")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil || len(raw) > maxAttachmentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Datei ist zu groß (max. 10 MB)")
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "Datei ist leer")
		return
	}

	// Typ am INHALT festmachen, nie am Dateinamen oder dem behaupteten Header.
	var data []byte
	var contentType string
	switch {
	case bytes.HasPrefix(raw, []byte("%PDF-")):
		data, contentType = raw, "application/pdf"
	default:
		d, ct, serr := sanitizeImage(raw)
		if serr != nil {
			if errors.Is(serr, errDecodeBusy) {
				writeError(w, http.StatusServiceUnavailable, "server busy, please retry shortly")
				return
			}
			writeError(w, http.StatusUnsupportedMediaType, "Erlaubt sind PDF, JPEG und PNG")
			return
		}
		data, contentType = d, ct
	}

	filename := trim(header.Filename)
	if len(filename) > 200 {
		filename = filename[:200]
	}
	var attID int64
	if err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO attachments (`+col+`, filename, content_type, byte_size, data)
		 VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		id, filename, contentType, len(data), data).Scan(&attID); err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusNotFound, "owner not found")
			return
		}
		serverError(w, r, "could not store attachment", err)
		return
	}
	// Nur Metadaten ins Protokoll — nie die Bytes.
	h.auditCreated(r, "attachment", attID, "Anhang hochgeladen: "+filename,
		map[string]any{col: id, "filename": filename, "content_type": contentType, "byte_size": len(data)})
	writeJSON(w, http.StatusCreated, attachmentMeta{
		ID: attID, Filename: filename, ContentType: contentType, ByteSize: len(data), CreatedAt: time.Now(),
	})
}

// GetAttachment liefert die Datei — als erzwungenen Download (siehe Kopfkommentar).
func (h *Handler) GetAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var ct, filename string
	var data []byte
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT content_type, filename, data FROM attachments WHERE id=$1`, id).Scan(&ct, &filename, &data); err != nil {
		writeError(w, http.StatusNotFound, "attachment not found")
		return
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct != "application/pdf" && ct != "image/jpeg" && ct != "image/png" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Dateiname RFC-5987-kodiert; das einfache filename= bleibt als ASCII-Fallback.
	safe := strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' || r > 0x7e {
			return '_'
		}
		return r
	}, filename)
	w.Header().Set("Content-Disposition", `attachment; filename="`+safe+`"`)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

// DeleteAttachment entfernt einen Anhang.
func (h *Handler) DeleteAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var delName string
	var delPerson, delVehicle *int64
	if err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM attachments WHERE id=$1 RETURNING filename, person_id, vehicle_id`, id).
		Scan(&delName, &delPerson, &delVehicle); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "attachment not found")
			return
		}
		serverError(w, r, "could not delete attachment", err)
		return
	}
	h.auditDeleted(r, "attachment", id, "Anhang gelöscht: "+delName,
		map[string]any{"filename": delName, "person_id": delPerson, "vehicle_id": delVehicle})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
