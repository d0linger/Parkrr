package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Warteliste (Ausbau 31): Interessenten, für die gerade kein Platz frei ist. Ein
// Eintrag ist noch keine Person; „Übernehmen“ legt die Person an und verknüpft sie.
// Einträge löschen sich nach 12 Monaten (server.expirySweeps), weil die Einwilligung
// nur für die Warteliste gilt.

type waitlistEntry struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Email        string     `json:"email"`
	Phone        string     `json:"phone"`
	CategoryID   *int64     `json:"category_id"`
	CategoryName string     `json:"category_name,omitempty"`
	LengthM      *float64   `json:"length_m"`
	WidthM       *float64   `json:"width_m"`
	WantedFrom   *time.Time `json:"wanted_from"`
	WantedUntil  *time.Time `json:"wanted_until"`
	Note         string     `json:"note"`
	Consent      bool       `json:"consent"`
	Status       string     `json:"status"`
	PersonID     *int64     `json:"person_id"`
	CreatedAt    time.Time  `json:"created_at"`
}

type waitlistIn struct {
	Name        string   `json:"name"`
	Email       string   `json:"email"`
	Phone       string   `json:"phone"`
	CategoryID  *int64   `json:"category_id"`
	LengthM     *float64 `json:"length_m"`
	WidthM      *float64 `json:"width_m"`
	WantedFrom  *string  `json:"wanted_from"`
	WantedUntil *string  `json:"wanted_until"`
	Note        string   `json:"note"`
	Consent     bool     `json:"consent"`
	Status      string   `json:"status"`
}

const waitlistCols = `w.id, w.name, w.email, w.phone, w.category_id, COALESCE(c.name, ''), w.length_m, w.width_m,
	w.wanted_from, w.wanted_until, w.note, w.consent, w.status, w.person_id, w.created_at`

func scanWaitlist(row rowScanner) (waitlistEntry, error) {
	var e waitlistEntry
	err := row.Scan(&e.ID, &e.Name, &e.Email, &e.Phone, &e.CategoryID, &e.CategoryName, &e.LengthM, &e.WidthM,
		&e.WantedFrom, &e.WantedUntil, &e.Note, &e.Consent, &e.Status, &e.PersonID, &e.CreatedAt)
	return e, err
}

// waitlistDay macht ein Datum für den Audit-Diff vergleichbar (Tag statt Zeitpunkt).
func waitlistDay(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

type parsedWaitlist struct {
	in          waitlistIn
	from, until *time.Time
}

func parseWaitlist(in waitlistIn) (*parsedWaitlist, string) {
	in.Name, in.Email, in.Phone, in.Note = trim(in.Name), trim(in.Email), trim(in.Phone), trim(in.Note)
	if in.Name == "" {
		return nil, "Name fehlt"
	}
	if !validNameLength(in.Name) || !validPhoneLength(in.Phone) || !validNoteLength(in.Note) {
		return nil, "Eingabe ist zu lang"
	}
	if in.Email != "" && !validEmail(in.Email) {
		return nil, "E-Mail ist ungültig"
	}
	if in.Email == "" && in.Phone == "" {
		return nil, "E-Mail oder Telefon angeben, sonst lässt sich niemand benachrichtigen"
	}
	if !in.Consent {
		return nil, "Ohne Einwilligung darf der Eintrag nicht gespeichert werden"
	}
	if (in.LengthM != nil && (*in.LengthM <= 0 || *in.LengthM > 60)) || (in.WidthM != nil && (*in.WidthM <= 0 || *in.WidthM > 15)) {
		return nil, "Maße ungültig"
	}
	from, err := parseOptDate(in.WantedFrom)
	if err != nil {
		return nil, "Gewünscht ab: Datum ungültig"
	}
	until, err := parseOptDate(in.WantedUntil)
	if err != nil {
		return nil, "Gewünscht bis: Datum ungültig"
	}
	if from != nil && until != nil && until.Before(*from) {
		return nil, "„bis“ liegt vor „ab“"
	}
	switch in.Status {
	case "":
		in.Status = "offen"
	case "offen", "uebernommen", "abgesagt":
	default:
		return nil, "Status ungültig"
	}
	return &parsedWaitlist{in: in, from: from, until: until}, ""
}

// ListWaitlist: GET /api/waitlist — offene zuerst, dann nach Eingang.
func (h *Handler) ListWaitlist(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT `+waitlistCols+` FROM waitlist w LEFT JOIN categories c ON c.id = w.category_id
		  ORDER BY (w.status = 'offen') DESC, w.created_at, w.id LIMIT 500`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []waitlistEntry{}
	for rows.Next() {
		e, err := scanWaitlist(rows)
		if err != nil {
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

func (h *Handler) getWaitlist(ctx context.Context, q dbQuerier, id int64) (waitlistEntry, error) {
	return scanWaitlist(q.QueryRow(ctx, `SELECT `+waitlistCols+` FROM waitlist w LEFT JOIN categories c ON c.id = w.category_id WHERE w.id=$1`, id))
}

// CreateWaitlist: POST /api/waitlist.
func (h *Handler) CreateWaitlist(w http.ResponseWriter, r *http.Request) {
	var in waitlistIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	pw, msg := parseWaitlist(in)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	var id int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO waitlist (name, email, phone, category_id, length_m, width_m, wanted_from, wanted_until, note, consent, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
		pw.in.Name, pw.in.Email, pw.in.Phone, pw.in.CategoryID, pw.in.LengthM, pw.in.WidthM, pw.from, pw.until, pw.in.Note, pw.in.Consent, pw.in.Status).Scan(&id)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "Tarif gibt es nicht")
			return
		}
		serverError(w, r, "could not save", err)
		return
	}
	h.auditCreated(r, "waitlist", id, "Wartelisten-Eintrag angelegt", map[string]any{"category_id": pw.in.CategoryID, "status": pw.in.Status})
	e, _ := h.getWaitlist(r.Context(), h.Pool, id)
	writeJSON(w, http.StatusCreated, e)
}

// UpdateWaitlist: PUT /api/waitlist/{id}.
func (h *Handler) UpdateWaitlist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in waitlistIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	pw, msg := parseWaitlist(in)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	old, err := h.getWaitlist(r.Context(), h.Pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE waitlist SET name=$2, email=$3, phone=$4, category_id=$5, length_m=$6, width_m=$7, wanted_from=$8,
		        wanted_until=$9, note=$10, consent=$11, status=$12, updated_at=now() WHERE id=$1`,
		id, pw.in.Name, pw.in.Email, pw.in.Phone, pw.in.CategoryID, pw.in.LengthM, pw.in.WidthM, pw.from, pw.until,
		pw.in.Note, pw.in.Consent, pw.in.Status); err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "Tarif gibt es nicht")
			return
		}
		serverError(w, r, "could not save", err)
		return
	}
	// Kontaktdaten (name, email, phone, note) nur als „geändert“, ohne Werte: die
	// Warteliste löscht sich nach 12 Monaten, das Änderungsprotokoll bleibt länger.
	summary := "Wartelisten-Eintrag geändert"
	if old.Name != pw.in.Name || old.Email != pw.in.Email || old.Phone != pw.in.Phone || old.Note != pw.in.Note {
		summary += " (Kontaktdaten geändert)"
	}
	h.auditChange(r, "update", "waitlist", id, summary, diffFields(
		map[string]any{"status": old.Status, "category_id": old.CategoryID, "length_m": old.LengthM, "width_m": old.WidthM,
			"wanted_from": waitlistDay(old.WantedFrom), "wanted_until": waitlistDay(old.WantedUntil), "consent": old.Consent},
		map[string]any{"status": pw.in.Status, "category_id": pw.in.CategoryID, "length_m": pw.in.LengthM, "width_m": pw.in.WidthM,
			"wanted_from": waitlistDay(pw.from), "wanted_until": waitlistDay(pw.until), "consent": pw.in.Consent}))
	e, _ := h.getWaitlist(r.Context(), h.Pool, id)
	writeJSON(w, http.StatusOK, e)
}

// DeleteWaitlist: DELETE /api/waitlist/{id}.
func (h *Handler) DeleteWaitlist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var status string
	var cat *int64
	err := h.Pool.QueryRow(r.Context(), `DELETE FROM waitlist WHERE id=$1 RETURNING status, category_id`, id).Scan(&status, &cat)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "could not delete", err)
		return
	}
	// Ohne Namen und Kontakt: der Eintrag ist gelöscht, auch aus dem Protokoll soll er
	// nicht wieder lesbar werden.
	h.auditDeleted(r, "waitlist", id, "Wartelisten-Eintrag gelöscht", map[string]any{"status": status, "category_id": cat})
	w.WriteHeader(http.StatusNoContent)
}

// ConvertWaitlist: POST /api/waitlist/{id}/convert — legt die Person an und markiert
// den Eintrag als übernommen. Das Gefährt legt die Oberfläche danach im gewohnten
// Formular an (vorbelegt), mit allen Prüfungen.
func (h *Handler) ConvertWaitlist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var pid int64
	err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT id FROM waitlist WHERE id=$1 FOR UPDATE`, id); err != nil {
			return err
		}
		e, err := h.getWaitlist(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if e.Status != "offen" {
			return errWaitlistNotOpen
		}
		first, last := splitName(e.Name)
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO persons (first_name, last_name, email, phone, notes) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			first, last, e.Email, e.Phone, strings.TrimSpace("Von der Warteliste übernommen. "+e.Note)).Scan(&pid); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE waitlist SET status='uebernommen', person_id=$2, updated_at=now() WHERE id=$1`, id, pid); err != nil {
			return err
		}
		return h.auditCreatedTx(r.Context(), tx, r, "person", pid, "Person aus der Warteliste angelegt", map[string]any{"waitlist_id": id})
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, errWaitlistNotOpen):
		writeError(w, http.StatusConflict, "Der Eintrag ist nicht mehr offen")
		return
	case err != nil:
		serverError(w, r, "could not convert", err)
		return
	}
	h.audit(r, "convert", "waitlist", id, "Wartelisten-Eintrag übernommen")
	writeJSON(w, http.StatusOK, map[string]any{"person_id": pid})
}

var errWaitlistNotOpen = errors.New("waitlist entry is not open")

// splitName teilt „Vorname Nachname“ am letzten Leerzeichen; ein einzelnes Wort ist
// der Nachname.
func splitName(s string) (string, string) {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.LastIndex(s, " "); i > 0 {
		return s[:i], s[i+1:]
	}
	return "", s
}

type waitlistMatch struct {
	CategoryID   int64  `json:"category_id"`
	CategoryName string `json:"category_name"`
	Waiting      int    `json:"waiting"`
	Freed        int    `json:"freed"`
}

// WaitlistSummary: GET /api/waitlist/summary — offene Einträge je Tarif und wie viele
// Gefährte dieses Tarifs in den letzten 14 Tagen abgeholt oder storniert wurden. Die
// Heute-Liste macht daraus „2 Wartende, 1 Platz frei geworden“.
func (h *Handler) WaitlistSummary(w http.ResponseWriter, r *http.Request) {
	var open int
	if err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM waitlist WHERE status='offen'`).Scan(&open); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT c.id, c.name, count(*),
		        (SELECT count(*) FROM vehicles v WHERE v.category_id = c.id AND v.status IN ('collected', 'cancelled')
		            AND v.end_date BETWEEN CURRENT_DATE - 14 AND CURRENT_DATE)
		   FROM waitlist wl JOIN categories c ON c.id = wl.category_id
		  WHERE wl.status = 'offen'
		  GROUP BY c.id, c.name ORDER BY c.name`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	matches := []waitlistMatch{}
	for rows.Next() {
		var m waitlistMatch
		if err := rows.Scan(&m.CategoryID, &m.CategoryName, &m.Waiting, &m.Freed); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"open": open, "by_category": matches})
}
