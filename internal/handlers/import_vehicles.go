package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Gefährte per CSV importieren (Ausbau 9). Derselbe Ablauf wie beim Personen-Import:
// Vorlage, eine Transaktion, Fehler je Zeile. Angelegt wird über dieselbe Prüfung und
// Anlage wie im Formular (validateVehicleRequest, insertVehicleTx), also mit
// festgeschriebenem Preis, Statusverlauf und Audit. Personen werden NICHT angelegt:
// zuerst Personen importieren, dann Gefährte.
var vehicleHeaderAliases = map[string]string{
	"email": "email", "e-mail": "email", "mail": "email",
	"person": "person", "kunde": "person", "mieter": "person", "name": "person",
	"vorname": "first", "first_name": "first", "nachname": "last", "last_name": "last",
	"kategorie": "category", "tarif": "category", "category": "category", "category_name": "category",
	"bezeichnung": "label", "label": "label", "gefährt": "label", "gefaehrt": "label", "fahrzeug": "label",
	"kennzeichen": "plate", "license_plate": "plate", "plate": "plate",
	"beginn": "start", "start": "start", "start_date": "start", "einlagerung": "start", "seit": "start",
	"ende": "end", "end": "end", "end_date": "end", "bis": "end",
	"abrechnung": "period", "periode": "period", "billing_period": "period",
	"preis": "rate", "miete": "rate", "rate": "rate", "satz": "rate",
	"status": "status",
	"notiz":  "notes", "notizen": "notes", "notes": "notes", "bemerkung": "notes",
	"länge": "length", "laenge": "length", "length_m": "length",
	"breite": "width", "width_m": "width",
	"höhe": "height", "hoehe": "height", "height_m": "height",
	"ladebedarf": "power", "strom": "power", "needs_power": "power",
}

// parseImportDate accepts ISO (2026-10-16) and the German form (16.10.2026).
func parseImportDate(s string) (string, bool) {
	s = trim(s)
	if s == "" {
		return "", false
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format("2006-01-02"), true
	}
	if t, err := time.Parse("2.1.2006", s); err == nil {
		return t.Format("2006-01-02"), true
	}
	return "", false
}

// parseImportNumber accepts "85", "85,50" and "85.50"; empty is "not given".
func parseImportNumber(s string) (*float64, error) {
	s = strings.ReplaceAll(trim(s), " ", "")
	if s == "" {
		return nil, nil
	}
	if strings.Count(s, ",") == 1 && !strings.Contains(s, ".") {
		s = strings.Replace(s, ",", ".", 1)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

var importPeriods = map[string]string{
	"": "monthly", "monatlich": "monthly", "monat": "monthly", "monthly": "monthly", "m": "monthly",
	"jährlich": "yearly", "jaehrlich": "yearly", "jahr": "yearly", "yearly": "yearly", "j": "yearly",
}

var importStatuses = map[string]string{
	"": "stored", "eingelagert": "stored", "stored": "stored",
	"reserviert": "reserved", "reserved": "reserved",
	"abgeholt": "collected", "collected": "collected",
}

func importTruthy(s string) bool {
	switch strings.ToLower(trim(s)) {
	case "ja", "j", "yes", "y", "1", "x", "true", "wahr":
		return true
	}
	return false
}

// normPlate makes plates comparable: upper case, no spaces or hyphens.
func normPlate(s string) string {
	return strings.NewReplacer(" ", "", "-", "", " ", "").Replace(strings.ToUpper(trim(s)))
}

func normName(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// VehicleImportTemplate serves the example CSV for the vehicle import.
func (h *Handler) VehicleImportTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="parkrr-gefaehrte-vorlage.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	_ = cw.Write([]string{"email", "person", "kategorie", "bezeichnung", "kennzeichen", "beginn", "ende", "abrechnung", "preis", "status", "länge", "breite", "höhe", "ladebedarf", "notiz"})
	rows := [][]string{
		{"max@example.com", "", "Wohnwagen", "Eriba Touring 542", "W-12345X", "01.11.2026", "31.03.2027", "monatlich", "", "eingelagert", "6,1", "2,2", "2,6", "nein", ""},
		{"", "Erika Musterfrau", "Motorrad", "BMW R 1250", "G-4711A", "15.10.2026", "", "jährlich", "420", "reserviert", "", "", "", "ja", "Erhaltungsladegerät"},
	}
	for _, row := range rows {
		for j := range row {
			row[j] = csvSafe(row[j])
		}
		_ = cw.Write(row)
	}
	cw.Flush()
}

// ImportVehicles bulk-creates vehicles from an uploaded CSV. Required per row: a
// person (e-mail, or full name when it is unique), a Tarif by name and a start date.
// A plate that an active Gefährt already carries (or an earlier row of the file) is
// skipped, so a repeated upload does not double the stock.
func (h *Handler) ImportVehicles(w http.ResponseWriter, r *http.Request) {
	records, ok := readImportCSV(w, r)
	if !ok {
		return
	}
	col := map[string]int{}
	for i, name := range records[0] {
		if field, ok := vehicleHeaderAliases[strings.ToLower(strings.TrimSpace(name))]; ok {
			if _, seen := col[field]; !seen {
				col[field] = i
			}
		}
	}
	_, hasEmail := col["email"]
	_, hasPerson := col["person"]
	_, hasLast := col["last"]
	_, hasCat := col["category"]
	_, hasStart := col["start"]
	if (!hasEmail && !hasPerson && !hasLast) || !hasCat || !hasStart {
		writeError(w, http.StatusBadRequest, "Spalten fehlen (erwartet: email oder person, kategorie, beginn)")
		return
	}
	get := func(rec []string, field string) string {
		if idx, ok := col[field]; ok && idx < len(rec) {
			return csvUnguard(trim(rec[idx]))
		}
		return ""
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Nachschlagetabellen einmal laden: Personen nach E-Mail und Name, Tarife nach Name,
	// Kennzeichen der aktiven Gefährte.
	byEmail, byName := map[string]int64{}, map[string][]int64{}
	prows, err := tx.Query(ctx, `SELECT id, lower(email), trim(first_name || ' ' || last_name) FROM persons WHERE NOT anonymized`)
	if err != nil {
		serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
		return
	}
	for prows.Next() {
		var id int64
		var email, name string
		if err := prows.Scan(&id, &email, &name); err != nil {
			prows.Close()
			serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
			return
		}
		if email != "" {
			if _, dup := byEmail[email]; !dup {
				byEmail[email] = id
			}
		}
		byName[normName(name)] = append(byName[normName(name)], id)
	}
	prows.Close()
	cats := map[string]int64{}
	crows, err := tx.Query(ctx, `SELECT id, lower(name) FROM categories ORDER BY archived, id`)
	if err != nil {
		serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
		return
	}
	for crows.Next() {
		var id int64
		var name string
		if err := crows.Scan(&id, &name); err != nil {
			crows.Close()
			serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
			return
		}
		if _, seen := cats[name]; !seen {
			cats[name] = id // ein aktiver Tarif gewinnt vor einem archivierten gleichen Namens
		}
	}
	crows.Close()
	plates := map[string]bool{}
	vrows, err := tx.Query(ctx, `SELECT license_plate FROM vehicles WHERE NOT archived AND btrim(license_plate) <> ''`)
	if err != nil {
		serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
		return
	}
	for vrows.Next() {
		var p string
		if err := vrows.Scan(&p); err != nil {
			vrows.Close()
			serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
			return
		}
		plates[normPlate(p)] = true
	}
	vrows.Close()

	res := importResult{Errors: []importRowError{}}
	fail := func(row int, msg string) {
		res.Failed++
		if len(res.Errors) < maxImportErrs {
			res.Errors = append(res.Errors, importRowError{Row: row, Message: msg})
		}
	}
	for i := 1; i < len(records); i++ {
		rec, rowNo := records[i], i+1
		if rowBlank(rec) {
			continue
		}
		// Person: E-Mail zuerst, sonst eindeutiger Name.
		var pid int64
		if e := strings.ToLower(get(rec, "email")); e != "" {
			pid = byEmail[e]
		}
		if pid == 0 {
			name := get(rec, "person")
			if name == "" {
				name = strings.TrimSpace(get(rec, "first") + " " + get(rec, "last"))
			}
			switch ids := byName[normName(name)]; {
			case name == "" && get(rec, "email") == "":
				fail(rowNo, "Person fehlt (E-Mail oder Name)")
				continue
			case len(ids) == 1:
				pid = ids[0]
			case len(ids) > 1:
				fail(rowNo, "Name „"+name+"“ ist nicht eindeutig, bitte die E-Mail angeben")
				continue
			}
		}
		if pid == 0 {
			fail(rowNo, "Person nicht gefunden, zuerst Personen importieren oder anlegen")
			continue
		}
		catID, ok := cats[strings.ToLower(get(rec, "category"))]
		if !ok {
			fail(rowNo, "Tarif „"+get(rec, "category")+"“ gibt es nicht")
			continue
		}
		start, ok := parseImportDate(get(rec, "start"))
		if !ok {
			fail(rowNo, "Beginn fehlt oder ist kein Datum (TT.MM.JJJJ)")
			continue
		}
		req := vehicleRequest{PersonID: pid, CategoryID: catID, Label: get(rec, "label"),
			LicensePlate: get(rec, "plate"), Notes: get(rec, "notes"), StartDate: start,
			NeedsPower: importTruthy(get(rec, "power"))}
		if s := get(rec, "end"); s != "" {
			end, ok := parseImportDate(s)
			if !ok {
				fail(rowNo, "Ende ist kein Datum (TT.MM.JJJJ)")
				continue
			}
			req.EndDate = &end
		}
		period, ok := importPeriods[strings.ToLower(get(rec, "period"))]
		if !ok {
			fail(rowNo, "Abrechnung muss monatlich oder jährlich sein")
			continue
		}
		req.BillingPeriod = period
		status, ok := importStatuses[strings.ToLower(get(rec, "status"))]
		if !ok {
			fail(rowNo, "Status muss eingelagert, reserviert oder abgeholt sein")
			continue
		}
		req.Status = status
		rate, err := parseImportNumber(get(rec, "rate"))
		if err != nil {
			fail(rowNo, "Preis ist keine Zahl")
			continue
		}
		req.Rate = rate
		dims := [3]*float64{}
		dimMax := [3]float64{60, 15, 15}
		dimBad := ""
		for k, field := range []string{"length", "width", "height"} {
			v, err := parseImportNumber(get(rec, field))
			if err != nil || (v != nil && (*v <= 0 || *v > dimMax[k])) {
				dimBad = map[string]string{"length": "Länge", "width": "Breite", "height": "Höhe"}[field]
				break
			}
			dims[k] = v
		}
		if dimBad != "" {
			fail(rowNo, dimBad+" ist keine gültige Angabe in Metern")
			continue
		}
		if np := normPlate(req.LicensePlate); np != "" && plates[np] {
			res.Skipped++
			continue
		}
		pv, err := h.validateVehicleRequest(ctx, req)
		if err != nil {
			fail(rowNo, err.Error())
			continue
		}
		id, err := h.insertVehicleTx(r, tx, pv)
		if err != nil {
			if errors.Is(err, errVehicleCategoryMissing) {
				fail(rowNo, "Tarif gibt es nicht")
				continue
			}
			serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
			return
		}
		if err := h.auditCreatedTx(ctx, tx, r, "vehicle", id, "created vehicle "+vehicleLabel(pv.req.Label, pv.req.LicensePlate)+" (CSV-Import)",
			vehicleCreatedSnapshot(pv)); err != nil {
			serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
			return
		}
		if dims[0] != nil || dims[1] != nil || dims[2] != nil {
			if _, err := tx.Exec(ctx, `UPDATE vehicles SET length_m=$1, width_m=$2, height_m=$3 WHERE id=$4`, dims[0], dims[1], dims[2], id); err != nil {
				serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
				return
			}
		}
		if np := normPlate(req.LicensePlate); np != "" {
			plates[np] = true
		}
		res.Imported++
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, r, "Import fehlgeschlagen – nichts wurde übernommen", err)
		return
	}
	h.audit(r, "import", "vehicle", 0,
		fmt.Sprintf("CSV-Import Gefährte: %d angelegt, %d übersprungen, %d Fehler", res.Imported, res.Skipped, res.Failed))
	writeJSON(w, http.StatusOK, res)
}
