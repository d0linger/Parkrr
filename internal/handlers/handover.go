package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

const (
	maxHandoverNotes = 4000

	// maxSignatureBytes ist die Grenze der DEKODIERTEN PNG-Daten — und sie muss unter
	// dem liegen, was als Base64 durch den JSON-Rumpf überhaupt hereinpasst.
	//
	// Mit glatt 1 MiB zielte sie daneben: die Unterschrift kommt base64-kodiert im
	// JSON an, und Base64 bläht um 4/3 auf. Ein 1-MiB-PNG braucht also rund 1,37 MiB
	// im Rumpf, den decodeJSON bei maxJSONBody abschneidet — die Prüfung unten wurde
	// nie erreicht, der Anwender bekam statt "Signatur zu groß" einen Syntaxfehler des
	// JSON-Lesers. Tatsächlich durchgingen nur rund 750 KiB. Die Grenze rechnet sich
	// daher aus dem Rumpf-Budget zurück: abzüglich der Notizen und etwas Luft für
	// Feldnamen, Anführungszeichen und den "data:"-Vorsatz, mal 3/4 für Base64.
	maxSignatureBytes = (maxJSONBody - maxHandoverNotes - 2048) / 4 * 3
)

// handoverMeta is the JSON view of a handover protocol (never the raw signature
// bytes — those are fetched separately as an image).
type handoverMeta struct {
	ID           int64            `json:"id"`
	VehicleID    int64            `json:"vehicle_id"`
	Direction    string           `json:"direction"`
	Notes        string           `json:"notes"`
	SignerName   string           `json:"signer_name"`
	HasSignature bool             `json:"has_signature"`
	CreatedAt    time.Time        `json:"created_at"`
	CreatedBy    string           `json:"created_by,omitempty"`
	Damages      []handoverDamage `json:"damages"`
	Photos       []handoverPhoto  `json:"photos"`
	// PhotosOpen: Fotos lassen sich noch ergänzen (bis handoverPhotoWindow nach dem Protokoll).
	PhotosOpen bool `json:"photos_open"`
}

// Zustandsfotos an der Übergabe (Ausbau 24). Feste Ansichten, damit Einlagerung und
// Abholung Bild für Bild nebeneinander stehen; "schaden" für Detailaufnahmen.
var handoverViews = []string{"vorne", "hinten", "links", "rechts", "innen", "km", "schaden"}

var handoverViewLabel = map[string]string{
	"vorne": "Vorne", "hinten": "Hinten", "links": "Links", "rechts": "Rechts",
	"innen": "Innenraum", "km": "Kilometerstand", "schaden": "Schaden",
}

const (
	// Fotos gehören zum Beleg. Nachreichen geht nur kurz nach dem Protokoll (der
	// Dialog lädt sie direkt nach dem Speichern hoch); danach ist der Satz geschlossen.
	handoverPhotoWindow = 24 * time.Hour
	maxHandoverPhotos   = 12
	maxHandoverDamages  = 30
	maxDamageAreaRunes  = 60
	maxDamageNoteRunes  = 300
)

type handoverDamage struct {
	Area string `json:"area"`
	Note string `json:"note"`
}

type handoverPhoto struct {
	ID   int64  `json:"id"`
	View string `json:"view"`
}

func validHandoverView(v string) bool {
	for _, x := range handoverViews {
		if x == v {
			return true
		}
	}
	return false
}

// cleanDamages trimmt die Schadenliste, verwirft leere Zeilen und prüft die Grenzen.
func cleanDamages(in []handoverDamage) ([]handoverDamage, string) {
	out := []handoverDamage{}
	for _, d := range in {
		d.Area, d.Note = trim(d.Area), trim(d.Note)
		if d.Area == "" && d.Note == "" {
			continue
		}
		if len([]rune(d.Area)) > maxDamageAreaRunes || len([]rune(d.Note)) > maxDamageNoteRunes {
			return nil, "Schadensangabe zu lang"
		}
		out = append(out, d)
	}
	if len(out) > maxHandoverDamages {
		return nil, "Zu viele Schadensangaben"
	}
	return out, ""
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
		Direction  string           `json:"direction"`
		Notes      string           `json:"notes"`
		SignerName string           `json:"signer_name"`
		Signature  string           `json:"signature"`
		Damages    []handoverDamage `json:"damages"`
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
	if !validNameLength(signerName) {
		writeError(w, http.StatusBadRequest, "Name zu lang")
		return
	}
	damages, msg := cleanDamages(req.Damages)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	damagesJSON, err := json.Marshal(damages)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not store protocol")
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

	// Der Halter wird beim Anlegen festgeschrieben (PRT-01): Portal, Zeitleiste,
	// PDF und Anonymisierung lesen ihn aus person_id, nie über den HEUTIGEN Halter
	// des Gefährts. FOR SHARE serialisiert gegen UpdateVehicle (FOR UPDATE), damit
	// ein gleichzeitiger Halterwechsel das neue Protokoll sieht und abgelehnt wird.
	var meta handoverMeta
	if err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO handover_protocols (vehicle_id, person_id, direction, notes, signer_name, signature, created_by, damages)
		 SELECT v.id, v.person_id, $2, $3, $4, $5, $6, $7
		   FROM vehicles v WHERE v.id = $1 FOR SHARE
		 RETURNING id, vehicle_id, direction, notes, signer_name, (signature IS NOT NULL), created_at`,
		id, direction, notes, signerName, sig, createdBy, damagesJSON,
	).Scan(&meta.ID, &meta.VehicleID, &meta.Direction, &meta.Notes, &meta.SignerName, &meta.HasSignature, &meta.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isForeignKeyViolation(err) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not store protocol")
		return
	}
	meta.Damages, meta.Photos, meta.PhotosOpen = damages, []handoverPhoto{}, true
	h.auditCreated(r, "handover", meta.ID, handoverDirectionLabel(direction)+" für Gefährt "+strconv.FormatInt(id, 10),
		map[string]any{"vehicle_id": id, "direction": direction, "signer_name": signerName, "damages": len(damages)})
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
		        (h.signature IS NOT NULL), h.created_at, COALESCE(u.username,''), h.damages
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
	cutoff := h.now().Add(-handoverPhotoWindow)
	for rows.Next() {
		var m handoverMeta
		if err := rows.Scan(&m.ID, &m.VehicleID, &m.Direction, &m.Notes, &m.SignerName,
			&m.HasSignature, &m.CreatedAt, &m.CreatedBy, &m.Damages); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		if m.Damages == nil {
			m.Damages = []handoverDamage{}
		}
		m.Photos, m.PhotosOpen = []handoverPhoto{}, m.CreatedAt.After(cutoff)
		out = append(out, m)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	rows.Close()
	photos, err := h.handoverPhotosByVehicle(r.Context(), id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	for i := range out {
		if ps := photos[out[i].ID]; ps != nil {
			out[i].Photos = ps
		}
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
	w.Header().Set("Cache-Control", "private, no-store")
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
	// protocol vanished (vehicle, direction, signer), never the biometric artifact.
	// Die Protokollfotos gehen per Kaskade mit (Ausbau 24); ihre Zahl steht im Eintrag.
	var delVehicle int64
	var delDirection, delSigner string
	var delPhotos int
	if err := h.Pool.QueryRow(r.Context(),
		`WITH ph AS (SELECT count(*) AS n FROM vehicle_photos WHERE handover_id = $1)
		 DELETE FROM handover_protocols WHERE id=$1 RETURNING vehicle_id, direction, signer_name, (SELECT n FROM ph)`, id).
		Scan(&delVehicle, &delDirection, &delSigner, &delPhotos); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "protocol not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete protocol")
		return
	}
	h.auditDeleted(r, "handover", id, "Übergabeprotokoll gelöscht ("+delDirection+")", map[string]any{
		"vehicle_id": delVehicle, "direction": delDirection, "signer_name": delSigner, "photos": delPhotos,
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
		vehicleID            int64
		damages              []handoverDamage
	)
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT hp.direction, hp.notes, hp.signer_name, hp.created_at, hp.signature, hp.vehicle_id, hp.damages,
		        COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), cat.name, 'Gefährt'),
		        COALESCE(v.license_plate,''),
		        COALESCE(trim(p.first_name || ' ' || p.last_name), '')
		   FROM handover_protocols hp
		   JOIN vehicles v ON v.id = hp.vehicle_id
		   JOIN categories cat ON cat.id = v.category_id
		   JOIN persons p ON p.id = hp.person_id
		  WHERE hp.id = $1`, id,
	).Scan(&direction, &notes, &signer, &createdAt, &sig, &vehicleID, &damages, &label, &plate, &person); err != nil {
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
	pdf, tr := newPDF() // eingebettete Unicode-Schrift; tr ist damit die Identität (Hundert 14)
	pdf.SetMargins(left, 18, left)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()

	pdf.SetFont(pdfFontName, "B", 17)
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
		pdf.SetFont(pdfFontName, "", 10)
		pdf.SetTextColor(90, 90, 90)
		pdf.CellFormat(40, 6, tr(m[0]), "", 0, "L", false, 0, "")
		pdf.SetFont(pdfFontName, "B", 10)
		pdf.SetTextColor(20, 20, 20)
		pdf.CellFormat(0, 6, tr(m[1]), "", 1, "L", false, 0, "")
	}
	pdf.Ln(4)

	pdf.SetFont(pdfFontName, "B", 11)
	pdf.CellFormat(0, 6, tr("Zustand / Anmerkungen"), "", 1, "L", false, 0, "")
	pdf.SetFont(pdfFontName, "", 10)
	pdf.SetTextColor(30, 30, 30)
	body := notes
	if strings.TrimSpace(body) == "" {
		body = "—"
	}
	pdf.MultiCell(usableW, 5, tr(body), "", "L", false)
	pdf.Ln(4)

	// Bei der Abholung zählt der Vergleich: was bei der Einlagerung schon vermerkt war.
	var before *handoverRef
	if direction == "auslagerung" {
		ref, err := h.previousEinlagerung(r.Context(), vehicleID, id, createdAt)
		if err != nil {
			serverError(w, r, "query failed", err)
			return
		}
		before = ref
	}
	writeDamageList(pdf, tr, "Schäden", damages)
	if before != nil {
		writeDamageList(pdf, tr, "Bei der Einlagerung am "+before.CreatedAt.Format("02.01.2006")+" vermerkt", before.Damages)
	}
	pdf.Ln(4)

	// Signature.
	pdf.SetFont(pdfFontName, "B", 11)
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
	pdf.SetFont(pdfFontName, "", 9)
	pdf.SetTextColor(90, 90, 90)
	sn := signer
	if sn == "" {
		sn = "Name / Unterschrift"
	}
	pdf.CellFormat(80, 5, tr(sn), "", 1, "L", false, 0, "")

	photos, err := h.handoverPhotoData(r.Context(), id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	var beforePhotos []handoverPhotoBytes
	if before != nil {
		beforePhotos, err = h.handoverPhotoData(r.Context(), before.ID)
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if len(photos) > 0 || len(beforePhotos) > 0 {
		writeContactSheet(pdf, tr, photos, beforePhotos, before)
	}

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

// handoverPhotosByVehicle liefert die Protokollfotos eines Gefährts je Protokoll.
func (h *Handler) handoverPhotosByVehicle(ctx context.Context, vehicleID int64) (map[int64][]handoverPhoto, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT handover_id, id, view FROM vehicle_photos
		  WHERE vehicle_id = $1 AND handover_id IS NOT NULL ORDER BY handover_id, id`, vehicleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]handoverPhoto{}
	for rows.Next() {
		var hid int64
		var p handoverPhoto
		if err := rows.Scan(&hid, &p.ID, &p.View); err != nil {
			return nil, err
		}
		out[hid] = append(out[hid], p)
	}
	return out, rows.Err()
}

// UploadHandoverPhoto: POST /api/handovers/{id}/photos (multipart: photo, view).
// Dieselbe Bereinigung wie die Galerie (Metadaten weg, nur echte JPEG/PNG); das Foto
// hängt am Protokoll und zählt nicht zur Galerie des Gefährts.
func (h *Handler) UploadHandoverPhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes+1024)
	// #nosec G120 -- the request body is capped by MaxBytesReader above, so the
	// multipart parse is bounded and cannot exhaust memory.
	if err := r.ParseMultipartForm(maxPhotoBytes + 1024); err != nil {
		writeMultipartError(w, err, "file too large")
		return
	}
	view := trim(r.FormValue("view"))
	if !validHandoverView(view) {
		writeError(w, http.StatusBadRequest, "Ansicht fehlt oder ist unbekannt")
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
		if errors.Is(err, errDecodeBusy) {
			writeError(w, http.StatusServiceUnavailable, "server busy, please retry shortly")
			return
		}
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}
	filename := trim(header.Filename)
	if rs := []rune(filename); len(rs) > 200 {
		filename = string(rs[:200])
	}
	var photoID, vehicleID int64
	var closed, full bool
	txErr := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		// FOR SHARE hält das Protokoll fest, ohne es zu ändern (es ist unveränderlich).
		// Die Beratungssperre zählt gleichzeitige Uploads nicht über die Grenze.
		var created time.Time
		if err := tx.QueryRow(r.Context(),
			`SELECT vehicle_id, created_at FROM handover_protocols WHERE id = $1 FOR SHARE`, id).Scan(&vehicleID, &created); err != nil {
			return err
		}
		if !created.After(h.now().Add(-handoverPhotoWindow)) {
			closed = true
			return nil
		}
		if _, err := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock(hashtext($1))`, "parkrr.handover.photos."+strconv.FormatInt(id, 10)); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM vehicle_photos WHERE handover_id = $1`, id).Scan(&n); err != nil {
			return err
		}
		if n >= maxHandoverPhotos {
			full = true
			return nil
		}
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO vehicle_photos (vehicle_id, filename, content_type, byte_size, data, sort_order, handover_id, view)
			 VALUES ($1, $2, $3, $4, $5, 0, $6, $7) RETURNING id`,
			vehicleID, filename, contentType, len(data), data, id, view).Scan(&photoID); err != nil {
			return err
		}
		// Metadata only — image bytes must never enter the audit log.
		return h.auditCreatedTx(r.Context(), tx, r, "photo", photoID, "Zustandsfoto ("+handoverViewLabel[view]+") zum Übergabeprotokoll",
			map[string]any{"vehicle_id": vehicleID, "handover_id": id, "view": view, "content_type": contentType, "byte_size": len(data)})
	})
	switch {
	case errors.Is(txErr, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "protocol not found")
	case txErr != nil:
		serverError(w, r, "could not store photo", txErr)
	case closed:
		writeError(w, http.StatusConflict, "Fotos lassen sich nur bis 24 Stunden nach dem Protokoll ergänzen.")
	case full:
		writeError(w, http.StatusConflict, "Höchstens 12 Fotos je Protokoll.")
	default:
		writeJSON(w, http.StatusCreated, handoverPhoto{ID: photoID, View: view})
	}
}

// handoverRef ist das Einlagerungsprotokoll, mit dem eine Abholung verglichen wird.
type handoverRef struct {
	ID        int64
	CreatedAt time.Time
	Damages   []handoverDamage
}

// previousEinlagerung: die letzte Einlagerung desselben Gefährts vor dieser Abholung.
func (h *Handler) previousEinlagerung(ctx context.Context, vehicleID, beforeID int64, at time.Time) (*handoverRef, error) {
	var ref handoverRef
	err := h.Pool.QueryRow(ctx,
		`SELECT id, created_at, damages FROM handover_protocols
		  WHERE vehicle_id = $1 AND direction = 'einlagerung' AND id <> $2 AND created_at <= $3
		  ORDER BY created_at DESC, id DESC LIMIT 1`, vehicleID, beforeID, at).Scan(&ref.ID, &ref.CreatedAt, &ref.Damages)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ref, nil
}

type handoverPhotoBytes struct {
	ID          int64
	View        string
	ContentType string
	Data        []byte
}

func (h *Handler) handoverPhotoData(ctx context.Context, handoverID int64) ([]handoverPhotoBytes, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT id, view, content_type, data FROM vehicle_photos WHERE handover_id = $1 ORDER BY id`, handoverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []handoverPhotoBytes
	for rows.Next() {
		var p handoverPhotoBytes
		if err := rows.Scan(&p.ID, &p.View, &p.ContentType, &p.Data); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func writeDamageList(pdf *fpdf.Fpdf, tr func(string) string, title string, damages []handoverDamage) {
	pdf.SetFont(pdfFontName, "B", 11)
	pdf.SetTextColor(20, 20, 20)
	pdf.CellFormat(0, 6, tr(title), "", 1, "L", false, 0, "")
	pdf.SetFont(pdfFontName, "", 10)
	pdf.SetTextColor(30, 30, 30)
	if len(damages) == 0 {
		pdf.CellFormat(0, 5, tr("keine"), "", 1, "L", false, 0, "")
		pdf.Ln(2)
		return
	}
	for _, d := range damages {
		line := d.Area
		if d.Note != "" {
			if line != "" {
				line += ": "
			}
			line += d.Note
		}
		pdf.MultiCell(170, 5, tr("• "+line), "", "L", false)
	}
	pdf.Ln(2)
}

// writeContactSheet hängt die Zustandsfotos als Kontaktbogen an. Bei einer Abholung
// mit Einlagerungsprotokoll stehen beide Aufnahmen derselben Ansicht nebeneinander.
func writeContactSheet(pdf *fpdf.Fpdf, tr func(string) string, now, before []handoverPhotoBytes, ref *handoverRef) {
	const left, colW, imgH, labelH = 20.0, 82.0, 58.0, 6.0
	pdf.AddPage()
	pdf.SetFont(pdfFontName, "B", 14)
	pdf.SetTextColor(20, 20, 20)
	pdf.CellFormat(0, 9, tr("Zustandsfotos"), "", 1, "L", false, 0, "")
	compare := ref != nil
	if compare {
		pdf.SetFont(pdfFontName, "", 9)
		pdf.SetTextColor(90, 90, 90)
		pdf.CellFormat(colW+6, 5, tr("Einlagerung "+ref.CreatedAt.Format("02.01.2006")), "", 0, "L", false, 0, "")
		pdf.CellFormat(colW, 5, tr("Diese Übergabe"), "", 1, "L", false, 0, "")
	}
	pdf.Ln(1)
	place := func(p handoverPhotoBytes, x, y float64) {
		typ := "JPG"
		if p.ContentType == "image/png" {
			typ = "PNG"
		}
		name := "ph" + strconv.FormatInt(p.ID, 10)
		opt := fpdf.ImageOptions{ImageType: typ, ReadDpi: false}
		info := pdf.RegisterImageOptionsReader(name, opt, bytes.NewReader(p.Data))
		if info == nil || pdf.Error() != nil {
			pdf.ClearError()
			return
		}
		w, hh := colW, colW*info.Height()/info.Width()
		if hh > imgH {
			w, hh = imgH*info.Width()/info.Height(), imgH
		}
		pdf.ImageOptions(name, x, y, w, hh, false, opt, 0, "")
	}
	type cell struct{ a, b *handoverPhotoBytes }
	var cells []cell
	if compare {
		// Ansicht für Ansicht: Einlagerung links, Abholung rechts; Schadensfotos danach.
		byView := func(list []handoverPhotoBytes) map[string][]*handoverPhotoBytes {
			m := map[string][]*handoverPhotoBytes{}
			for i := range list {
				m[list[i].View] = append(m[list[i].View], &list[i])
			}
			return m
		}
		bv, nv := byView(before), byView(now)
		for _, v := range handoverViews {
			for i := 0; i < len(bv[v]) || i < len(nv[v]); i++ {
				var c cell
				if i < len(bv[v]) {
					c.a = bv[v][i]
				}
				if i < len(nv[v]) {
					c.b = nv[v][i]
				}
				cells = append(cells, c)
			}
		}
	} else {
		for i := 0; i < len(now); i += 2 {
			c := cell{a: &now[i]}
			if i+1 < len(now) {
				c.b = &now[i+1]
			}
			cells = append(cells, c)
		}
	}
	for _, c := range cells {
		if pdf.GetY()+labelH+imgH > 297-18 {
			pdf.AddPage()
		}
		y := pdf.GetY()
		pdf.SetFont(pdfFontName, "", 9)
		pdf.SetTextColor(60, 60, 60)
		pair := []*handoverPhotoBytes{c.a, c.b}
		for i, p := range pair {
			x := left + float64(i)*(colW+6)
			label := ""
			if p != nil {
				label = handoverViewLabel[p.View]
			} else if compare && pair[1-i] != nil {
				label = handoverViewLabel[pair[1-i].View] + " – nicht aufgenommen"
			}
			pdf.SetXY(x, y)
			pdf.CellFormat(colW, labelH, tr(label), "", 0, "L", false, 0, "")
			if p != nil {
				place(*p, x, y+labelH)
			}
		}
		pdf.SetXY(left, y+labelH+imgH+4)
	}
}
