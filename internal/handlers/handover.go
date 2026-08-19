package handlers

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

const (
	maxSignatureBytes = 1 << 20 // 1 MiB decoded PNG cap for a signature
	maxHandoverNotes  = 4000
)

// handoverMeta is the JSON view of a handover protocol (never the raw signature
// bytes — those are fetched separately as an image).
type handoverMeta struct {
	ID           int64     `json:"id"`
	VehicleID    int64     `json:"vehicle_id"`
	Direction    string    `json:"direction"`
	Notes        string    `json:"notes"`
	SignerName   string    `json:"signer_name"`
	HasSignature bool      `json:"has_signature"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by,omitempty"`
}

func handoverDirectionLabel(d string) string {
	switch d {
	case "einlagerung":
		return "Einlagerung (Übernahme)"
	case "auslagerung":
		return "Auslagerung (Rückgabe)"
	default:
		return d
	}
}

// decodeSignature accepts an optional "data:image/png;base64,…" URL (or bare
// base64) and returns clean PNG bytes, or nil when empty.
func decodeSignature(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, "data:") {
		if i := strings.IndexByte(s, ','); i >= 0 {
			s = s[i+1:]
		}
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errImage("ungültige Signatur (kein Base64)")
	}
	if len(raw) > maxSignatureBytes {
		return nil, errImage("Signatur zu groß")
	}
	clean, ct, err := sanitizeImage(raw)
	if err != nil {
		return nil, err
	}
	if ct != "image/png" {
		return nil, errImage("Signatur muss ein PNG sein")
	}
	return clean, nil
}

// CreateHandover records a handover protocol for a vehicle (editor+).
func (h *Handler) CreateHandover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Direction  string `json:"direction"`
		Notes      string `json:"notes"`
		SignerName string `json:"signer_name"`
		Signature  string `json:"signature"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	direction := trim(req.Direction)
	if direction != "einlagerung" && direction != "auslagerung" {
		writeError(w, http.StatusBadRequest, "direction muss 'einlagerung' oder 'auslagerung' sein")
		return
	}
	notes := trim(req.Notes)
	if len(notes) > maxHandoverNotes {
		writeError(w, http.StatusBadRequest, "Notizen zu lang")
		return
	}
	signerName := trim(req.SignerName)
	if len(signerName) > 200 {
		writeError(w, http.StatusBadRequest, "Name zu lang")
		return
	}
	sig, err := decodeSignature(req.Signature)
	if err != nil {
		if errors.Is(err, errDecodeBusy) {
			writeError(w, http.StatusServiceUnavailable, "server busy, please retry shortly")
			return
		}
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}

	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}

	var meta handoverMeta
	if err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO handover_protocols (vehicle_id, direction, notes, signer_name, signature, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, vehicle_id, direction, notes, signer_name, (signature IS NOT NULL), created_at`,
		id, direction, notes, signerName, sig, createdBy,
	).Scan(&meta.ID, &meta.VehicleID, &meta.Direction, &meta.Notes, &meta.SignerName, &meta.HasSignature, &meta.CreatedAt); err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not store protocol")
		return
	}
	h.auditCreated(r, "handover", meta.ID, handoverDirectionLabel(direction)+" für Gefährt "+strconv.FormatInt(id, 10),
		map[string]any{"vehicle_id": id, "direction": direction, "signer_name": signerName})
	writeJSON(w, http.StatusCreated, meta)
}

// ListHandovers returns a vehicle's handover protocols, newest first.
func (h *Handler) ListHandovers(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT h.id, h.vehicle_id, h.direction, h.notes, h.signer_name,
		        (h.signature IS NOT NULL), h.created_at, COALESCE(u.username,'')
		   FROM handover_protocols h
		   LEFT JOIN users u ON u.id = h.created_by
		  WHERE h.vehicle_id = $1
		  ORDER BY h.created_at DESC, h.id DESC`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []handoverMeta{}
	for rows.Next() {
		var m handoverMeta
		if err := rows.Scan(&m.ID, &m.VehicleID, &m.Direction, &m.Notes, &m.SignerName,
			&m.HasSignature, &m.CreatedAt, &m.CreatedBy); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, m)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GetHandoverSignature streams the signature PNG for a protocol.
func (h *Handler) GetHandoverSignature(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var data []byte
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT signature FROM handover_protocols WHERE id=$1`, id).Scan(&data); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "protocol not found")
		} else {
			writeError(w, http.StatusInternalServerError, "query failed")
		}
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusNotFound, "no signature")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(data)
}

// DeleteHandover removes a protocol (editor+).
func (h *Handler) DeleteHandover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	// The signature image is deliberately not read back — the trail records WHICH
	// protocol vanished (vehicle, direction, signer), never the biometric artefact.
	var delVehicle int64
	var delDirection, delSigner string
	if err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM handover_protocols WHERE id=$1 RETURNING vehicle_id, direction, signer_name`, id).
		Scan(&delVehicle, &delDirection, &delSigner); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "protocol not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete protocol")
		return
	}
	h.auditDeleted(r, "handover", id, "Übergabeprotokoll gelöscht ("+delDirection+")", map[string]any{
		"vehicle_id": delVehicle, "direction": delDirection, "signer_name": delSigner,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// HandoverPDF renders a one-page handover protocol as an A4 PDF, embedding the
// signature image when present.
func (h *Handler) HandoverPDF(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var (
		direction, notes     string
		signer, plate, label string
		person               string
		createdAt            time.Time
		sig                  []byte
	)
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT hp.direction, hp.notes, hp.signer_name, hp.created_at, hp.signature,
		        COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), cat.name, 'Gefährt'),
		        COALESCE(v.license_plate,''),
		        COALESCE(trim(p.first_name || ' ' || p.last_name), '')
		   FROM handover_protocols hp
		   JOIN vehicles v ON v.id = hp.vehicle_id
		   JOIN categories cat ON cat.id = v.category_id
		   JOIN persons p ON p.id = v.person_id
		  WHERE hp.id = $1`, id,
	).Scan(&direction, &notes, &signer, &createdAt, &sig, &label, &plate, &person); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "protocol not found")
		} else {
			writeError(w, http.StatusInternalServerError, "query failed")
		}
		return
	}

	const (
		left    = 20.0
		usableW = 170.0
	)
	pdf := fpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetMargins(left, 18, left)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 17)
	pdf.SetTextColor(20, 20, 20)
	pdf.CellFormat(0, 10, tr("Übergabeprotokoll"), "", 1, "L", false, 0, "")
	pdf.Ln(2)

	meta := [][2]string{
		{"Art", handoverDirectionLabel(direction)},
		{"Datum", createdAt.Format("02.01.2006 15:04")},
		{"Gefährt", label},
	}
	if plate != "" {
		meta = append(meta, [2]string{"Kennzeichen", plate})
	}
	if person != "" {
		meta = append(meta, [2]string{"Halter/in", person})
	}
	for _, m := range meta {
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(90, 90, 90)
		pdf.CellFormat(40, 6, tr(m[0]), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(20, 20, 20)
		pdf.CellFormat(0, 6, tr(m[1]), "", 1, "L", false, 0, "")
	}
	pdf.Ln(4)

	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 6, tr("Zustand / Anmerkungen"), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(30, 30, 30)
	body := notes
	if strings.TrimSpace(body) == "" {
		body = "—"
	}
	pdf.MultiCell(usableW, 5, tr(body), "", "L", false)
	pdf.Ln(8)

	// Signature.
	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetTextColor(20, 20, 20)
	pdf.CellFormat(0, 6, tr("Unterschrift"), "", 1, "L", false, 0, "")
	if len(sig) > 0 {
		opt := fpdf.ImageOptions{ImageType: "PNG", ReadDpi: false}
		pdf.RegisterImageOptionsReader("sig", opt, bytes.NewReader(sig))
		y := pdf.GetY() + 2
		pdf.ImageOptions("sig", left, y, 70, 0, false, opt, 0, "")
		pdf.SetY(y + 30)
	} else {
		pdf.Ln(16)
	}
	pdf.SetDrawColor(120, 120, 120)
	pdf.Line(left, pdf.GetY(), left+80, pdf.GetY())
	pdf.Ln(1)
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(90, 90, 90)
	sn := signer
	if sn == "" {
		sn = "Name / Unterschrift"
	}
	pdf.CellFormat(80, 5, tr(sn), "", 1, "L", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		writeError(w, http.StatusInternalServerError, "PDF konnte nicht erstellt werden")
		return
	}
	fname := "Uebergabeprotokoll-" + strconv.FormatInt(id, 10) + ".pdf"
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Header().Set("Content-Disposition", `inline; filename="`+fname+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
