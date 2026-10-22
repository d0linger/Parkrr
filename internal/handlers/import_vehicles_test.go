package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseImportHelpers(t *testing.T) {
	for in, want := range map[string]string{"16.10.2026": "2026-10-16", "1.2.2027": "2027-02-01", "2026-10-16": "2026-10-16"} {
		if got, ok := parseImportDate(in); !ok || got != want {
			t.Errorf("parseImportDate(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := parseImportDate("morgen"); ok {
		t.Error("kein Datum darf nicht durchgehen")
	}
	for in, want := range map[string]float64{"85": 85, "85,50": 85.5, "6.1": 6.1} {
		if got, err := parseImportNumber(in); err != nil || got == nil || *got != want {
			t.Errorf("parseImportNumber(%q) = %v, %v", in, got, err)
		}
	}
	if got, err := parseImportNumber(""); err != nil || got != nil {
		t.Error("leer heißt: nicht angegeben")
	}
	if normPlate(" w-12 345x ") != "W12345X" {
		t.Error("normPlate")
	}
}

// Rows resolve person (e-mail or unique name) and Tarif by name, lock the Tarif
// price, take dimensions, skip a plate that already exists and report bad rows.
func TestImportVehicles(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	email := "veh.imp." + tag + "@example.com"
	var pid int64
	if err := h.Pool.QueryRow(ctx, `INSERT INTO persons (first_name, last_name, email) VALUES ('Vera', 'Integration', $1) RETURNING id`, email).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	catName := "ImpWohnwagen" + tag
	if _, err := h.Pool.Exec(ctx, `INSERT INTO categories (name, default_monthly_cost, default_yearly_cost) VALUES ($1, 65, 700)`, catName); err != nil {
		t.Fatal(err)
	}
	plate := "IMP-" + tag[len(tag)-6:]
	csvData := "\ufeffemail;kategorie;bezeichnung;kennzeichen;beginn;abrechnung;länge;breite;ladebedarf\n" +
		email + ";" + catName + ";Eriba;" + plate + ";01.11.2026;monatlich;6,1;2,2;ja\n" +
		email + ";" + catName + ";Doppelt;" + strings.ToLower(plate) + ";01.11.2026;;;;\n" + // gleiches Kennzeichen -> übersprungen
		email + ";GibtEsNicht;X;;01.11.2026;;;;\n" + // Tarif fehlt
		"nobody@example.invalid;" + catName + ";Y;;01.11.2026;;;;\n" + // Person fehlt
		email + ";" + catName + ";Z;;kein-datum;;;;\n" + // Datum falsch
		email + ";" + catName + ";L;;01.11.2026;;99;;\n" // Länge unmöglich
	w := httptest.NewRecorder()
	h.ImportVehicles(w, csvUpload(t, csvData))
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var res importResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Imported != 1 || res.Skipped != 1 || res.Failed != 4 {
		t.Fatalf("imported/skipped/failed = %d/%d/%d, want 1/1/4: %+v", res.Imported, res.Skipped, res.Failed, res.Errors)
	}
	rows := map[int]bool{}
	for _, e := range res.Errors {
		rows[e.Row] = true
	}
	for _, row := range []int{4, 5, 6, 7} {
		if !rows[row] {
			t.Errorf("row %d missing from errors: %+v", row, res.Errors)
		}
	}
	var rate, length float64
	var power bool
	var status string
	if err := h.Pool.QueryRow(ctx, `SELECT rate, length_m, needs_power, status FROM vehicles WHERE person_id=$1 AND license_plate=$2`, pid, plate).Scan(&rate, &length, &power, &status); err != nil {
		t.Fatal(err)
	}
	if rate != 65 || length != 6.1 || !power || status != "stored" {
		t.Errorf("imported vehicle rate=%v length=%v power=%v status=%s", rate, length, power, status)
	}
	var history int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM vehicle_status_history sc JOIN vehicles v ON v.id = sc.vehicle_id WHERE v.license_plate=$1`, plate).Scan(&history); err != nil || history != 1 {
		t.Errorf("status history rows = %d (%v), want 1 like a form-created vehicle", history, err)
	}

	// Ohne Pflichtspalten wird die Datei abgelehnt.
	w = httptest.NewRecorder()
	h.ImportVehicles(w, csvUpload(t, "bezeichnung;kennzeichen\nA;B\n"))
	if w.Code != 400 {
		t.Errorf("missing columns: %d, want 400", w.Code)
	}
}
