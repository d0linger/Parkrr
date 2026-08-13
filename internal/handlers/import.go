package handlers

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	maxImportBytes = 2 << 20 // 2 MiB CSV upload cap
	maxImportRows  = 5000    // data rows (excludes header)
	maxImportErrs  = 100     // detail rows returned; total still counted in Failed
)

// importRowError reports a single row that could not be imported.
type importRowError struct {
	Row     int    `json:"row"` // 1-based line in the file (header = row 1)
	Message string `json:"message"`
}

// importResult summarises a bulk import. Errors carries up to maxImportErrs
// detail rows; Failed is the full count so the UI never under-reports.
type importResult struct {
	Imported int              `json:"imported"`
	Skipped  int              `json:"skipped"` // duplicates (existing email)
	Failed   int              `json:"failed"`
	Errors   []importRowError `json:"errors"`
}

// Header aliases → canonical field. Matches the CSV-Export headers so an
// exported "personen.csv" round-trips, plus common English/German variants.
// An "id" column (present in exports) has no alias and is simply ignored.
var personHeaderAliases = map[string]string{
	"vorname": "first", "first_name": "first", "firstname": "first", "first": "first",
	"nachname": "last", "last_name": "last", "lastname": "last", "last": "last", "name": "last",
	"email": "email", "e-mail": "email", "mail": "email",
	"telefon": "phone", "phone": "phone", "tel": "phone", "mobil": "phone",
	"adresse": "address", "address": "address", "anschrift": "address",
	"notiz": "notes", "notizen": "notes", "notes": "notes", "note": "notes", "bemerkung": "notes",
}

// ImportPersons bulk-creates persons from an uploaded CSV (multipart "file").
// The delimiter (';' or ',') and a leading UTF-8 BOM are auto-detected; columns
// are mapped by header name (German export headers or English aliases). Invalid
// rows are skipped and reported and duplicate e-mails are skipped — the import
// is best-effort per row, not all-or-nothing.
func (h *Handler) ImportPersons(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+1024)
	// #nosec G120 -- the request body is capped by MaxBytesReader above, so the
	// multipart parse is bounded and cannot exhaust memory.
	if err := r.ParseMultipartForm(maxImportBytes + 1024); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "Datei zu groß")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxImportBytes+1))
	if err != nil || len(raw) > maxImportBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Datei zu groß")
		return
	}

	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	if len(bytes.TrimSpace(raw)) == 0 {
		writeError(w, http.StatusBadRequest, "leere Datei")
		return
	}

	// Detect the delimiter from the header line: ';' (German Excel) else ','.
	firstLine := raw
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		firstLine = raw[:i]
	}
	comma := ','
	if bytes.Count(firstLine, []byte{';'}) > bytes.Count(firstLine, []byte{','}) {
		comma = ';'
	}

	cr := csv.NewReader(bytes.NewReader(raw))
	cr.Comma = comma
	cr.FieldsPerRecord = -1 // tolerate ragged rows; we index defensively
	cr.TrimLeadingSpace = true
	records, err := cr.ReadAll()
	if err != nil {
		writeError(w, http.StatusBadRequest, "CSV konnte nicht gelesen werden")
		return
	}
	if len(records) == 0 {
		writeError(w, http.StatusBadRequest, "leere Datei")
		return
	}
	if len(records) > maxImportRows+1 {
		writeError(w, http.StatusRequestEntityTooLarge, "zu viele Zeilen")
		return
	}

	// Map header names → column index (first match wins).
	col := map[string]int{}
	for i, name := range records[0] {
		key := strings.ToLower(strings.TrimSpace(name))
		if field, ok := personHeaderAliases[key]; ok {
			if _, seen := col[field]; !seen {
				col[field] = i
			}
		}
	}
	if _, hasFirst := col["first"]; !hasFirst {
		if _, hasLast := col["last"]; !hasLast {
			writeError(w, http.StatusBadRequest, "keine erkennbaren Spalten (erwartet: vorname/nachname, email, telefon, adresse)")
			return
		}
	}
	get := func(rec []string, field string) string {
		if idx, ok := col[field]; ok && idx < len(rec) {
			return trim(rec[idx])
		}
		return ""
	}

	res := importResult{Errors: []importRowError{}}
	ctx := r.Context()
	for i := 1; i < len(records); i++ {
		rec := records[i]
		rowNo := i + 1 // 1-based including the header row

		blank := true
		for _, c := range rec {
			if trim(c) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue // ignore empty lines silently
		}

		req := personRequest{
			FirstName: get(rec, "first"),
			LastName:  get(rec, "last"),
			Email:     get(rec, "email"),
			Phone:     get(rec, "phone"),
			Address:   get(rec, "address"),
			Notes:     get(rec, "notes"),
		}
		req.normalize()
		if msg := req.validate(); msg != "" {
			res.Failed++
			if len(res.Errors) < maxImportErrs {
				res.Errors = append(res.Errors, importRowError{Row: rowNo, Message: msg})
			}
			continue
		}
		// Skip a person whose (non-empty) e-mail already exists, case-insensitive.
		if req.Email != "" {
			var exists bool
			if err := h.Pool.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM persons WHERE lower(email) = lower($1))`, req.Email).Scan(&exists); err == nil && exists {
				res.Skipped++
				continue
			}
		}
		if _, err := h.Pool.Exec(ctx,
			`INSERT INTO persons (first_name, last_name, email, phone, address, notes)
			 VALUES ($1,$2,$3,$4,$5,$6)`,
			req.FirstName, req.LastName, req.Email, req.Phone, req.Address, req.Notes); err != nil {
			res.Failed++
			if len(res.Errors) < maxImportErrs {
				res.Errors = append(res.Errors, importRowError{Row: rowNo, Message: "Datenbankfehler"})
			}
			continue
		}
		res.Imported++
	}

	h.audit(r, "import", "person", 0,
		fmt.Sprintf("CSV-Import: %d angelegt, %d übersprungen, %d Fehler", res.Imported, res.Skipped, res.Failed))
	writeJSON(w, http.StatusOK, res)
}
