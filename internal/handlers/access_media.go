package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// Schlüssel- und Handsenderbuch (Ausbau 34): wer welches Zugangsmittel zur Halle hat.
// Gespeichert werden nur Art und Nummer, nie ein Code im Klartext. Ausgabe und Rückgabe
// hängen an der Person; die Abholung zeigt, was noch draußen ist.

var accessKindLabel = map[string]string{
	"schluessel": "Schlüssel", "handsender": "Handsender", "karte": "Karte", "code": "Code",
}

type accessMedium struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Label      string     `json:"label"`
	HallID     *int64     `json:"hall_id"`
	HallName   string     `json:"hall_name,omitempty"`
	Note       string     `json:"note"`
	Retired    bool       `json:"retired"`
	LoanID     *int64     `json:"loan_id"`
	PersonID   *int64     `json:"person_id"`
	PersonName string     `json:"person_name,omitempty"`
	IssuedOn   *time.Time `json:"issued_on"`
	LoanCount  int        `json:"loan_count"`
}

type accessMediumIn struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	HallID  *int64 `json:"hall_id"`
	Note    string `json:"note"`
	Retired bool   `json:"retired"`
}

const accessMediumSelect = `SELECT m.id, m.kind, m.label, m.hall_id, COALESCE(hl.name, ''), m.note, m.retired,
	       l.id, l.person_id, COALESCE(trim(p.first_name || ' ' || p.last_name), ''), l.issued_on,
	       (SELECT count(*) FROM access_media_loans x WHERE x.medium_id = m.id)
	  FROM access_media m
	  LEFT JOIN halls hl ON hl.id = m.hall_id
	  LEFT JOIN access_media_loans l ON l.medium_id = m.id AND l.returned_on IS NULL
	  LEFT JOIN persons p ON p.id = l.person_id`

func scanAccessMedium(row rowScanner) (accessMedium, error) {
	var m accessMedium
	err := row.Scan(&m.ID, &m.Kind, &m.Label, &m.HallID, &m.HallName, &m.Note, &m.Retired,
		&m.LoanID, &m.PersonID, &m.PersonName, &m.IssuedOn, &m.LoanCount)
	return m, err
}

func (h *Handler) getAccessMedium(ctx context.Context, id int64) (accessMedium, error) {
	return scanAccessMedium(h.Pool.QueryRow(ctx, accessMediumSelect+` WHERE m.id = $1`, id))
}

func parseAccessMedium(in *accessMediumIn) string {
	in.Label, in.Note = trim(in.Label), trim(in.Note)
	if _, ok := accessKindLabel[in.Kind]; !ok {
		return "Unbekannte Art"
	}
	if in.Label == "" {
		return "Nummer oder Bezeichnung fehlt"
	}
	if len([]rune(in.Label)) > 60 || !validNoteLength(in.Note) {
		return "Eingabe ist zu lang"
	}
	return ""
}

// ListAccessMedia: GET /api/access-media.
func (h *Handler) ListAccessMedia(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(), accessMediumSelect+` ORDER BY m.retired, m.kind, lower(m.label), m.id`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []accessMedium{}
	for rows.Next() {
		m, err := scanAccessMedium(rows)
		if err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateAccessMedium: POST /api/access-media.
func (h *Handler) CreateAccessMedium(w http.ResponseWriter, r *http.Request) {
	var in accessMediumIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg := parseAccessMedium(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var id int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO access_media (kind, label, hall_id, note, retired) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		in.Kind, in.Label, in.HallID, in.Note, in.Retired).Scan(&id)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, accessKindLabel[in.Kind]+" "+in.Label+" gibt es schon")
		return
	}
	if isForeignKeyViolation(err) {
		writeError(w, http.StatusBadRequest, "Halle gibt es nicht")
		return
	}
	if err != nil {
		serverError(w, r, "could not create", err)
		return
	}
	h.auditCreated(r, "access_medium", id, accessKindLabel[in.Kind]+" "+in.Label+" angelegt",
		map[string]any{"kind": in.Kind, "label": in.Label, "hall_id": in.HallID, "retired": in.Retired})
	m, err := h.getAccessMedium(r.Context(), id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

// UpdateAccessMedium: PUT /api/access-media/{id}. Ausmustern (retired) geht nur, wenn
// das Zugangsmittel gerade nicht ausgegeben ist.
func (h *Handler) UpdateAccessMedium(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in accessMediumIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg := parseAccessMedium(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	old, err := h.getAccessMedium(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if in.Retired && old.LoanID != nil {
		writeError(w, http.StatusConflict, "Erst zurücknehmen, dann ausmustern: noch ausgegeben an "+old.PersonName)
		return
	}
	_, err = h.Pool.Exec(r.Context(),
		`UPDATE access_media SET kind=$2, label=$3, hall_id=$4, note=$5, retired=$6, updated_at=now() WHERE id=$1`,
		id, in.Kind, in.Label, in.HallID, in.Note, in.Retired)
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, accessKindLabel[in.Kind]+" "+in.Label+" gibt es schon")
		return
	}
	if isForeignKeyViolation(err) {
		writeError(w, http.StatusBadRequest, "Halle gibt es nicht")
		return
	}
	if err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	next := old
	next.Kind, next.Label, next.HallID, next.Note, next.Retired = in.Kind, in.Label, in.HallID, in.Note, in.Retired
	h.auditChange(r, "update", "access_medium", id, accessKindLabel[in.Kind]+" "+in.Label+" geändert",
		diffFields(old, next, "hall_name", "loan_id", "person_id", "person_name", "issued_on", "loan_count"))
	m, err := h.getAccessMedium(r.Context(), id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// DeleteAccessMedium: DELETE /api/access-media/{id}. Nur ohne Ausgaben: wer es je
// hatte, bleibt nachvollziehbar (dafür gibt es „ausmustern“).
func (h *Handler) DeleteAccessMedium(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var kind, label string
	err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM access_media m WHERE m.id = $1
		   AND NOT EXISTS (SELECT 1 FROM access_media_loans l WHERE l.medium_id = m.id)
		 RETURNING kind, label`, id).Scan(&kind, &label)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if qerr := h.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM access_media WHERE id = $1)`, id).Scan(&exists); qerr == nil && exists {
			writeError(w, http.StatusConflict, "Wurde schon ausgegeben – ausmustern statt löschen, damit nachvollziehbar bleibt, wer es hatte.")
			return
		}
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "could not delete", err)
		return
	}
	h.auditDeleted(r, "access_medium", id, accessKindLabel[kind]+" "+label+" gelöscht",
		map[string]any{"kind": kind, "label": label})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// IssueAccessMedium: POST /api/access-media/{id}/issue {person_id, issued_on?, note?}.
func (h *Handler) IssueAccessMedium(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		PersonID int64   `json:"person_id"`
		IssuedOn *string `json:"issued_on"`
		Note     string  `json:"note"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.Note = trim(in.Note)
	if in.PersonID <= 0 || !validNoteLength(in.Note) {
		writeError(w, http.StatusBadRequest, "Person fehlt oder Notiz zu lang")
		return
	}
	issued := startOfDayUTC(h.now())
	if in.IssuedOn != nil && trim(*in.IssuedOn) != "" {
		d, err := parseOptDate(in.IssuedOn)
		if err != nil || d == nil {
			writeError(w, http.StatusBadRequest, "issued_on must be YYYY-MM-DD")
			return
		}
		issued = *d
	}
	m, err := h.getAccessMedium(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if m.Retired {
		writeError(w, http.StatusConflict, "Ausgemustert – nicht mehr ausgeben")
		return
	}
	var loanID int64
	err = h.Pool.QueryRow(r.Context(),
		`INSERT INTO access_media_loans (medium_id, person_id, issued_on, note)
		 SELECT $1, p.id, $3, $4 FROM persons p WHERE p.id = $2 AND NOT p.anonymized RETURNING id`,
		id, in.PersonID, issued, in.Note).Scan(&loanID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusBadRequest, "Person gibt es nicht")
		return
	}
	if isUniqueViolation(err) {
		writeError(w, http.StatusConflict, "Schon ausgegeben – erst zurücknehmen")
		return
	}
	if err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	h.auditCreated(r, "access_loan", loanID, accessKindLabel[m.Kind]+" "+m.Label+" ausgegeben",
		map[string]any{"medium_id": id, "person_id": in.PersonID, "issued_on": issued.Format(dateLayout)})
	out, err := h.getAccessMedium(r.Context(), id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// ReturnAccessMedium: POST /api/access-media/{id}/return {returned_on?}.
func (h *Handler) ReturnAccessMedium(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		ReturnedOn *string `json:"returned_on"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	returned := startOfDayUTC(h.now())
	if in.ReturnedOn != nil && trim(*in.ReturnedOn) != "" {
		d, err := parseOptDate(in.ReturnedOn)
		if err != nil || d == nil {
			writeError(w, http.StatusBadRequest, "returned_on must be YYYY-MM-DD")
			return
		}
		returned = *d
	}
	var loanID, personID int64
	var kind, label string
	err := h.Pool.QueryRow(r.Context(),
		`UPDATE access_media_loans l SET returned_on = GREATEST($2::date, l.issued_on)
		   FROM access_media m
		  WHERE l.medium_id = $1 AND l.returned_on IS NULL AND m.id = l.medium_id
		 RETURNING l.id, l.person_id, m.kind, m.label`, id, returned).Scan(&loanID, &personID, &kind, &label)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "Ist gerade nicht ausgegeben")
		return
	}
	if err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	h.auditChange(r, "return", "access_loan", loanID, accessKindLabel[kind]+" "+label+" zurückgenommen",
		diffFields(map[string]any{"returned_on": nil}, map[string]any{"returned_on": returned.Format(dateLayout), "person_id": personID}))
	out, err := h.getAccessMedium(r.Context(), id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type accessLoan struct {
	ID         int64      `json:"id"`
	MediumID   int64      `json:"medium_id"`
	Kind       string     `json:"kind"`
	Label      string     `json:"label"`
	PersonID   int64      `json:"person_id"`
	PersonName string     `json:"person_name"`
	IssuedOn   time.Time  `json:"issued_on"`
	ReturnedOn *time.Time `json:"returned_on"`
	Note       string     `json:"note"`
}

func (h *Handler) listAccessLoans(ctx context.Context, where string, arg int64) ([]accessLoan, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT l.id, l.medium_id, m.kind, m.label, l.person_id, trim(p.first_name || ' ' || p.last_name),
		        l.issued_on, l.returned_on, l.note
		   FROM access_media_loans l
		   JOIN access_media m ON m.id = l.medium_id
		   JOIN persons p ON p.id = l.person_id
		  WHERE `+where+`
		  ORDER BY l.issued_on DESC, l.id DESC LIMIT 200`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []accessLoan{}
	for rows.Next() {
		var l accessLoan
		if err := rows.Scan(&l.ID, &l.MediumID, &l.Kind, &l.Label, &l.PersonID, &l.PersonName,
			&l.IssuedOn, &l.ReturnedOn, &l.Note); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// AccessMediumLoans: GET /api/access-media/{id}/loans — wer es wann hatte.
func (h *Handler) AccessMediumLoans(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	out, err := h.listAccessLoans(r.Context(), `l.medium_id = $1`, id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PersonAccessMedia: GET /api/persons/{id}/access-media — was die Person noch hat.
func (h *Handler) PersonAccessMedia(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	out, err := h.listAccessLoans(r.Context(), `l.person_id = $1 AND l.returned_on IS NULL`, id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// openAccessLoans nennt, was eine Person noch an Zugangsmitteln hat (DeletePerson):
// mit der Person verschwände sonst still der Nachweis, dass ein Schlüssel draußen ist.
func (h *Handler) openAccessLoans(ctx context.Context, personID int64) (string, error) {
	var names string
	err := h.Pool.QueryRow(ctx,
		`SELECT COALESCE(string_agg(CASE m.kind WHEN 'schluessel' THEN 'Schlüssel' WHEN 'handsender' THEN 'Handsender'
		                                         WHEN 'karte' THEN 'Karte' ELSE 'Code' END || ' ' || m.label, ', ' ORDER BY m.label), '')
		   FROM access_media_loans l JOIN access_media m ON m.id = l.medium_id
		  WHERE l.person_id = $1 AND l.returned_on IS NULL`, personID).Scan(&names)
	return names, err
}
