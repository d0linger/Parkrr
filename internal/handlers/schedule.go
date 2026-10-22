package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// Termine aus dem Betrieb (Ausbau 10 und 11), eine Quelle für die Wochenliste und
// das Kalender-Abo. Dieselben Regeln wie der Kalender in der App:
//   - Abholung    = Vertragsende eines nicht stornierten Gefährts,
//   - Einlagerung = Reservierungsbeginn eines nicht stornierten Gefährts,
//   - Abholwunsch = offener Portal-Wunsch mit Wunschtermin (hängt an der Person).
type scheduleEvent struct {
	Date       time.Time
	Kind       string // pickup | arrival | wish | leave | return (Ruhezeit)
	VehicleID  int64  // 0 bei einem Abholwunsch
	SourceID   int64  // Ruhezeit bzw. Portal-Wunsch; bleibt bei Terminänderungen stabil
	PersonID   int64
	Label      string
	Plate      string
	PersonName string
	Hall       string
	Spot       string
	Note       string
	Time       string // HH:MM eines Abholwunschs im Zeitfenster (Ausbau 23), sonst leer
}

var scheduleKindLabel = map[string]string{"pickup": "Abholung", "arrival": "Einlagerung", "wish": "Abholwunsch",
	"leave": "Kurz weg", "return": "Rückkehr"}

// scheduleEvents liefert alle Termine mit from <= Datum <= to, sortiert nach Datum,
// Halle, Platz und Gefährt.
func (h *Handler) scheduleEvents(ctx context.Context, from, to time.Time) ([]scheduleEvent, error) {
	var out []scheduleEvent
	rows, err := h.Pool.Query(ctx,
		`SELECT k.kind, k.day, v.id, v.person_id,
		        COALESCE(NULLIF(v.label, ''), c.name), v.license_plate,
		        trim(p.first_name || ' ' || p.last_name),
		        COALESCE(hl.name, ''), COALESCE(s.label, ''), 0::bigint
		   FROM vehicles v
		   JOIN persons p ON p.id = v.person_id
		   JOIN categories c ON c.id = v.category_id
		   LEFT JOIN spots s ON s.id = v.spot_id
		   LEFT JOIN halls hl ON hl.id = s.hall_id
		   CROSS JOIN LATERAL (VALUES ('pickup', v.end_date), ('arrival', v.reserved_from)) AS k(kind, day)
		  WHERE v.status <> 'cancelled' AND k.day IS NOT NULL AND k.day BETWEEN $1 AND $2
		 UNION ALL
		 -- Ruhezeit: Abfahrt am ersten Ruhetag, Rückkehr am Tag nach der geplanten
		 -- (bzw. tatsächlichen) letzten Ruhetag.
		 SELECT k.kind, k.day, v.id, v.person_id,
		        COALESCE(NULLIF(v.label, ''), c.name), v.license_plate,
		        trim(p.first_name || ' ' || p.last_name),
		        COALESCE(hl.name, ''), COALESCE(s.label, ''), vp.id
		   FROM vehicle_pauses vp
		   JOIN vehicles v ON v.id = vp.vehicle_id
		   JOIN persons p ON p.id = v.person_id
		   JOIN categories c ON c.id = v.category_id
		   LEFT JOIN spots s ON s.id = v.spot_id
		   LEFT JOIN halls hl ON hl.id = s.hall_id
		   CROSS JOIN LATERAL (VALUES ('leave', vp.start_date), ('return', vp.until + 1)) AS k(kind, day)
		  WHERE k.day IS NOT NULL AND k.day BETWEEN $1 AND $2`, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e scheduleEvent
		if err := rows.Scan(&e.Kind, &e.Date, &e.VehicleID, &e.PersonID, &e.Label, &e.Plate, &e.PersonName, &e.Hall, &e.Spot, &e.SourceID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Der Wunschtermin steht als Text im JSON; nur gültige Daten im Fenster zählen.
	wrows, err := h.Pool.Query(ctx,
		`SELECT pr.id, pr.person_id, trim(p.first_name || ' ' || p.last_name),
		        COALESCE(pr.payload->>'date', ''), COALESCE(pr.payload->>'note', ''), COALESCE(pr.payload->>'time', '')
		   FROM portal_requests pr JOIN persons p ON p.id = pr.person_id
		  WHERE pr.kind = 'pickup' AND pr.status = 'offen'`)
	if err != nil {
		return nil, err
	}
	for wrows.Next() {
		var e scheduleEvent
		var day string
		if err := wrows.Scan(&e.SourceID, &e.PersonID, &e.PersonName, &day, &e.Note, &e.Time); err != nil {
			wrows.Close()
			return nil, err
		}
		d, perr := time.Parse("2006-01-02", day)
		if perr != nil || d.Before(from) || d.After(to) {
			continue
		}
		e.Date, e.Kind = d, "wish"
		out = append(out, e)
	}
	wrows.Close()
	if err := wrows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.Date.Equal(b.Date) {
			return a.Date.Before(b.Date)
		}
		if a.Time != b.Time {
			return a.Time < b.Time
		}
		if a.Hall != b.Hall {
			return a.Hall < b.Hall
		}
		if a.Spot != b.Spot {
			return a.Spot < b.Spot
		}
		return a.VehicleID < b.VehicleID
	})
	return out, nil
}

// weekStart ist der Montag der Woche, in die d fällt (als UTC-Mitternacht wie DATE-Spalten).
func weekStart(d time.Time) time.Time {
	y, m, day := d.Date()
	t := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	return t.AddDate(0, 0, -((int(t.Weekday()) + 6) % 7))
}

var germanWeekday = [...]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}

// WeekSchedulePDF: GET /api/reports/week.pdf?from=YYYY-MM-DD. Die Woche (Montag bis
// Sonntag), in die from fällt, Standard heute; sortiert nach Tag, Halle und Platz, damit
// der Rundgang einer Reihenfolge folgt.
func (h *Handler) WeekSchedulePDF(w http.ResponseWriter, r *http.Request) {
	ref := h.now()
	if q := r.URL.Query().Get("from"); q != "" {
		d, err := time.Parse("2006-01-02", q)
		if err != nil || len(q) != 10 {
			writeError(w, http.StatusBadRequest, "from must be YYYY-MM-DD")
			return
		}
		ref = d
	}
	from := weekStart(ref)
	to := from.AddDate(0, 0, 6)
	events, err := h.scheduleEvents(r.Context(), from, to)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}

	pdf, tr := newPDF()
	pdf.SetMargins(14, 14, 14)
	pdf.SetAutoPageBreak(true, 14)
	pdf.AddPageFormat("L", pdf.GetPageSizeStr("A4"))
	pdf.SetFont(pdfFontName, "B", 15)
	pdf.SetTextColor(20, 20, 20)
	pdf.CellFormat(0, 8, tr("Woche "+from.Format("02.01.")+" bis "+to.Format("02.01.2006")), "", 1, "L", false, 0, "")
	pdf.SetFont(pdfFontName, "", 9)
	pdf.SetTextColor(110, 110, 110)
	pdf.CellFormat(0, 5, tr("Abholungen, Einlagerungen und offene Abholwünsche · Stand "+h.now().Format("02.01.2006 15:04")), "", 1, "L", false, 0, "")
	pdf.Ln(3)

	cols := []struct {
		title string
		w     float64
	}{{"Tag", 22}, {"Art", 26}, {"Person", 52}, {"Gefährt", 62}, {"Kennzeichen", 30}, {"Halle · Platz", 42}, {"Notiz", 35}}
	head := func() {
		pdf.SetFont(pdfFontName, "B", 9)
		pdf.SetFillColor(235, 238, 242)
		pdf.SetTextColor(40, 40, 40)
		for i, c := range cols {
			ln := 0
			if i == len(cols)-1 {
				ln = 1
			}
			pdf.CellFormat(c.w, 7, tr(c.title), "", ln, "L", true, 0, "")
		}
		pdf.SetFont(pdfFontName, "", 9)
		pdf.SetTextColor(20, 20, 20)
	}
	head()
	if len(events) == 0 {
		pdf.SetTextColor(110, 110, 110)
		pdf.CellFormat(0, 8, tr("In dieser Woche stehen keine Abholungen oder Einlagerungen an."), "", 1, "L", false, 0, "")
	}
	for _, e := range events {
		if pdf.GetY() > 190 {
			pdf.AddPageFormat("L", pdf.GetPageSizeStr("A4"))
			head()
		}
		place := e.Hall
		if e.Spot != "" {
			if place != "" {
				place += " · "
			}
			place += e.Spot
		}
		if place == "" && e.Kind != "wish" {
			place = "nicht platziert"
		}
		label := e.Label
		if e.Kind == "wish" {
			label = "aus dem Portal"
		}
		day := germanWeekday[e.Date.Weekday()] + " " + e.Date.Format("02.01.")
		if e.Time != "" {
			day += " " + e.Time
		}
		vals := []string{day, scheduleKindLabel[e.Kind], e.PersonName, label, e.Plate, place, e.Note}
		for i, c := range cols {
			ln := 0
			if i == len(cols)-1 {
				ln = 1
			}
			pdf.CellFormat(c.w, 6.5, tr(fitText(pdf, vals[i], c.w-1)), "B", ln, "L", false, 0, "")
		}
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="parkrr-woche-`+from.Format("2006-01-02")+`.pdf"`)
	if err := pdf.Output(w); err != nil {
		auth.SetRequestError(r.Context(), err)
		slog.Error("wochenliste-pdf: Ausgabe fehlgeschlagen", "err", err)
	}
}

// ---------- Kalender-Abo ----------
//
// Kalender-Apps kennen nur eine URL, deshalb steht der Schlüssel hier (anders als
// beim Portal, SEC-01) im Pfad. Ausgleich: der Feed enthält weder Namen noch Beträge,
// nur Gefährt, Kennzeichen und Halle; gespeichert wird nur der Hash; das Abo ist
// jederzeit widerrufbar; das Zugriffsprotokoll schwärzt den Pfad (server.logPath).

func feedHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (h *Handler) feedURL(r *http.Request, token string) string {
	base := strings.TrimRight(h.PublicBaseURL, "/")
	if base == "" {
		scheme := "http"
		if h.Auth != nil && h.Auth.RequestIsHTTPS(r) {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + "/ical/" + token
}

// CalendarFeedStatus: GET /api/calendar/feed — ob der angemeldete Benutzer ein Abo hat.
func (h *Handler) CalendarFeedStatus(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	var created time.Time
	err := h.Pool.QueryRow(r.Context(), `SELECT created_at FROM calendar_feeds WHERE user_id=$1`, u.ID).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"active": false})
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "created_at": created})
}

// CalendarFeedCreate: POST /api/calendar/feed — erzeugt (oder ersetzt) den Schlüssel und
// gibt die Abo-Adresse genau einmal zurück.
func (h *Handler) CalendarFeedCreate(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		serverError(w, r, "could not create token", err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if _, err := h.Pool.Exec(r.Context(),
		`INSERT INTO calendar_feeds (user_id, token_hash) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, created_at = now()`,
		u.ID, feedHash(token)); err != nil {
		serverError(w, r, "could not save token", err)
		return
	}
	h.audit(r, "create", "calendar_feed", u.ID, "Kalender-Abo erzeugt")
	writeJSON(w, http.StatusCreated, map[string]any{"url": h.feedURL(r, token)})
}

// CalendarFeedRevoke: DELETE /api/calendar/feed — widerruft das Abo.
func (h *Handler) CalendarFeedRevoke(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	var created time.Time
	err := h.Pool.QueryRow(r.Context(), `DELETE FROM calendar_feeds WHERE user_id=$1 RETURNING created_at`, u.ID).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent) // nichts zu widerrufen
		return
	}
	if err != nil {
		serverError(w, r, "could not revoke", err)
		return
	}
	h.auditDeleted(r, "calendar_feed", u.ID, "Kalender-Abo widerrufen", map[string]any{"user_id": u.ID, "created_at": created})
	w.WriteHeader(http.StatusNoContent)
}

// CalendarFeed: GET /ical/{token} (öffentlich). 30 Tage zurück bis ein Jahr voraus.
func (h *Handler) CalendarFeed(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	token = strings.TrimSuffix(token, ".ics")
	if len(token) < 20 || len(token) > 100 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var uid int64
	err := h.Pool.QueryRow(r.Context(),
		`SELECT f.user_id FROM calendar_feeds f JOIN users u ON u.id = f.user_id
		  WHERE f.token_hash = $1 AND NOT u.disabled`, feedHash(token)).Scan(&uid)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			serverError(w, r, "query failed", err)
			return
		}
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	y, m, d := h.now().Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	events, err := h.scheduleEvents(r.Context(), today.AddDate(0, 0, -30), today.AddDate(1, 0, 0))
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=900")
	_, _ = w.Write([]byte(h.renderICS(r, events)))
}

// icsText maskiert Text nach RFC 5545 (Backslash, Semikolon, Komma, Zeilenumbruch).
func icsText(s string) string {
	s = strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", "").Replace(s)
	return s
}

// icsFold bricht Zeilen nach 75 Bytes um, ohne ein UTF-8-Zeichen zu teilen.
func icsFold(line string) string {
	if len(line) <= 75 {
		return line + "\r\n"
	}
	var b strings.Builder
	limit := 75
	for len(line) > limit {
		cut := limit
		for cut > 0 && (line[cut]&0xC0) == 0x80 {
			cut--
		}
		b.WriteString(line[:cut])
		b.WriteString("\r\n ")
		line = line[cut:]
		limit = 74 // die Folgezeile beginnt mit einem Leerzeichen
	}
	b.WriteString(line)
	b.WriteString("\r\n")
	return b.String()
}

func (h *Handler) renderICS(r *http.Request, events []scheduleEvent) string {
	var b strings.Builder
	write := func(s string) { b.WriteString(icsFold(s)) }
	write("BEGIN:VCALENDAR")
	write("VERSION:2.0")
	write("PRODID:-//Parkrr//Kalender-Abo//DE")
	write("CALSCALE:GREGORIAN")
	write("X-WR-CALNAME:Parkrr")
	stamp := h.now().UTC().Format("20060102T150405Z")
	base := strings.TrimSuffix(h.vehicleLink(r, 0), "/#/vehicles/0")
	for _, e := range events {
		// Abholung/Einlagerung sind je Gefährt eindeutig. Ruhezeiten und Wünsche
		// brauchen zusätzlich ihren Datensatz, damit weitere Termine nicht kollidieren.
		uid := e.Kind + "-" + strconv.FormatInt(e.VehicleID, 10)
		if e.Kind == "leave" || e.Kind == "return" {
			uid += "-" + strconv.FormatInt(e.SourceID, 10)
		}
		summary := scheduleKindLabel[e.Kind] + ": " + e.Label
		if e.Plate != "" {
			summary += " (" + e.Plate + ")"
		}
		link := base + "/#/vehicles/" + strconv.FormatInt(e.VehicleID, 10)
		if e.Kind == "wish" {
			uid = "wish-" + strconv.FormatInt(e.PersonID, 10) + "-" + strconv.FormatInt(e.SourceID, 10)
			summary = "Abholwunsch aus dem Portal"
			if e.Time != "" {
				summary += " um " + e.Time
			}
			link = base + "/#/person/" + strconv.FormatInt(e.PersonID, 10)
		}
		place := e.Hall
		if e.Spot != "" {
			if place != "" {
				place += " · "
			}
			place += "Platz " + e.Spot
		}
		write("BEGIN:VEVENT")
		write("UID:" + uid + "@parkrr")
		write("DTSTAMP:" + stamp)
		write("DTSTART;VALUE=DATE:" + e.Date.Format("20060102"))
		write("DTEND;VALUE=DATE:" + e.Date.AddDate(0, 0, 1).Format("20060102"))
		write("SUMMARY:" + icsText(summary))
		if place != "" {
			write("LOCATION:" + icsText(place))
		}
		write("DESCRIPTION:" + icsText("In Parkrr öffnen: "+link))
		write("TRANSP:TRANSPARENT")
		write("END:VEVENT")
	}
	write("END:VCALENDAR")
	return b.String()
}
