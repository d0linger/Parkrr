package handlers

import (
	"context"
	"net/http"
	"strconv"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// Doppeleintrag-Hinweis in den Dialogen „Person“ und „Gefährt“. Er lief bisher über die
// Suche der Befehlspalette; die ist absichtlich unscharf (Trigramm-Ähnlichkeit, dazu
// Adresse und Notiz) und meldete so beim Bearbeiten von „Günter Kitzmüller“ einen
// „Wolfgang Atzmüller“. Ein Doppel ist hier ein gleicher Name, eine gleiche E-Mail, eine
// gleiche Telefonnummer oder ein gleiches Kennzeichen: normalisiert, aber nicht ähnlich.

const (
	duplicateLimit = 3
	// Telefonnummern werden über ihre Ziffern verglichen, und zwar die letzten acht:
	// „+43 664 1234567“ und „0664/1234567“ sind dieselbe Nummer.
	phoneTailDigits = 8
	minPhoneDigits  = 6
	// Kennzeichen zählen nur mit Buchstaben und Ziffern; kürzer ist kein Vergleich wert.
	minPlateChars = 2
)

// duplicateExclude liest den optionalen Parameter exclude (das gerade bearbeitete
// Objekt). 0 heißt: nichts ausschließen.
func duplicateExclude(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	if !validNumericParamLength(raw) {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

// duplicateRows führt eine Doppel-Abfrage aus und sammelt höchstens duplicateLimit Treffer.
func (h *Handler) duplicateRows(ctx context.Context, query string, mk func(pgx.Rows) (searchResult, error), args ...any) ([]searchResult, error) {
	rows, err := h.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]searchResult, 0, duplicateLimit)
	for rows.Next() {
		res, err := mk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, rows.Err()
}

// PersonDuplicates: GET /api/persons/duplicates?first_name=&last_name=&email=&phone=&exclude=
// Gleicher Name heißt gleicher Vor- und Nachname (Groß-/Kleinschreibung egal, auch
// vertauscht); anonymisierte Personen zählen nicht.
func (h *Handler) PersonDuplicates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	first, last := trim(q.Get("first_name")), trim(q.Get("last_name"))
	email, phone := trim(q.Get("email")), trim(q.Get("phone"))
	if !validNameLength(first) || !validNameLength(last) || !validEmailLength(email) || !validPhoneLength(phone) {
		writeError(w, http.StatusBadRequest, "parameter is too long")
		return
	}
	exclude, ok := duplicateExclude(q.Get("exclude"))
	if !ok {
		writeError(w, http.StatusBadRequest, "exclude must be an id")
		return
	}
	digits := onlyDigits(phone)
	if len(digits) < minPhoneDigits {
		digits = ""
	}
	if first == "" && last == "" && email == "" && digits == "" {
		writeJSON(w, http.StatusOK, []searchResult{})
		return
	}
	caps := h.searchCapabilities(r.Context())
	name := func(expr string) string { return caps.fold("lower(btrim(" + expr + "))") }
	sameName := `(` + name("p.first_name") + ` = ` + name("$1::text") + ` AND ` + name("p.last_name") + ` = ` + name("$2::text") +
		` OR ` + name("p.first_name") + ` = ` + name("$2::text") + ` AND ` + name("p.last_name") + ` = ` + name("$1::text") + `)`
	sameEmail := `$3::text <> '' AND lower(btrim(p.email)) = lower($3::text)`
	pDigits := `regexp_replace(p.phone, '\D', '', 'g')`
	tail := strconv.Itoa(phoneTailDigits)
	samePhone := `$4::text <> '' AND length(` + pDigits + `) >= ` + strconv.Itoa(minPhoneDigits) +
		` AND right(` + pDigits + `, ` + tail + `) = right($4::text, ` + tail + `)`
	out, err := h.duplicateRows(r.Context(), `
		SELECT p.id, p.first_name, p.last_name, p.email, p.phone,
		       CASE WHEN ($1::text <> '' OR $2::text <> '') AND `+sameName+` THEN 'gleicher Name'
		            WHEN `+sameEmail+` THEN 'gleiche E-Mail'
		            ELSE 'gleiche Telefonnummer' END
		  FROM persons p
		 WHERE p.id <> $5::bigint AND NOT p.anonymized
		   AND (($1::text <> '' OR $2::text <> '') AND `+sameName+` OR `+sameEmail+` OR `+samePhone+`)
		 ORDER BY p.last_name, p.first_name, p.id
		 LIMIT `+strconv.Itoa(duplicateLimit), func(rows pgx.Rows) (searchResult, error) {
		var id int64
		var f, l, em, ph, why string
		if err := rows.Scan(&id, &f, &l, &em, &ph, &why); err != nil {
			return searchResult{}, err
		}
		label := trim(f + " " + l)
		if label == "" {
			label = "(ohne Namen)"
		}
		return searchResult{Kind: "person", Label: label, Sub: dotJoin(why, em, ph),
			URL: "#/person/" + strconv.FormatInt(id, 10)}, nil
	}, first, last, email, digits, exclude)
	if err != nil {
		serverError(w, r, "duplicate check failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// VehicleDuplicates: GET /api/vehicles/duplicates?license_plate=&exclude=
// Gleiches Kennzeichen heißt: gleiche Buchstaben und Ziffern, ohne Leer- und
// Bindestriche. Frühere Saisons („Erneut einstellen“) stehen mit ihrem Status dabei.
func (h *Handler) VehicleDuplicates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	plate := trim(q.Get("license_plate"))
	if !validNameLength(plate) {
		writeError(w, http.StatusBadRequest, "license_plate is too long")
		return
	}
	exclude, ok := duplicateExclude(q.Get("exclude"))
	if !ok {
		writeError(w, http.StatusBadRequest, "exclude must be an id")
		return
	}
	if len([]rune(plateKey(plate))) < minPlateChars {
		writeJSON(w, http.StatusOK, []searchResult{})
		return
	}
	norm := func(expr string) string { return `upper(regexp_replace(` + expr + `, '[^[:alnum:]]', '', 'g'))` }
	out, err := h.duplicateRows(r.Context(), `
		SELECT v.id, v.label, v.license_plate, concat_ws(' ', p.first_name, p.last_name), v.archived, v.status
		  FROM vehicles v
		  JOIN persons p ON p.id = v.person_id
		 WHERE v.id <> $2::bigint AND v.license_plate <> ''
		   AND `+norm("v.license_plate")+` = `+norm("$1::text")+`
		 ORDER BY v.archived, v.status IN ('collected', 'cancelled'), v.id DESC
		 LIMIT `+strconv.Itoa(duplicateLimit), func(rows pgx.Rows) (searchResult, error) {
		var id int64
		var label, pl, owner, status string
		var archived bool
		if err := rows.Scan(&id, &label, &pl, &owner, &archived, &status); err != nil {
			return searchResult{}, err
		}
		title := label
		if title == "" {
			title = pl
		}
		state := ""
		switch {
		case archived:
			state = "archiviert"
		case status == "collected":
			state = "abgeholt"
		case status == "cancelled":
			state = "storniert"
		}
		return searchResult{Kind: "vehicle", Label: title, Sub: dotJoin(state, pl, owner),
			URL: "#/vehicle/" + strconv.FormatInt(id, 10)}, nil
	}, plate, exclude)
	if err != nil {
		serverError(w, r, "duplicate check failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func onlyDigits(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b = append(b, s[i])
		}
	}
	return string(b)
}

// plateKey entspricht der Normalisierung im SQL (nur Buchstaben und Ziffern); sie dient
// nur der Mindestlänge vor der Abfrage.
func plateKey(s string) string {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			out = append(out, c)
		}
	}
	return string(out)
}
