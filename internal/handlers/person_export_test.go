package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Ausbau 28: jede Tabelle mit Personenbezug (Spalte person_id oder owner_person_id)
// muss im Auskunftspaket vorkommen. Wer eine neue Tabelle mit person_id anlegt und sie
// hier vergisst, bekommt einen roten Test statt einer unvollständigen Auskunft.
func TestPersonExportCoversEveryPersonTable(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	rows, err := h.Pool.Query(ctx,
		`SELECT DISTINCT table_name FROM information_schema.columns
		  WHERE table_schema = 'public' AND column_name IN ('person_id', 'owner_person_id')`)
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{"persons": true}
	for _, tb := range personExportTables() {
		covered[tb] = true
	}
	var missing []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	rows.Close()
	if len(missing) > 0 {
		t.Fatalf("Tabellen mit Personenbezug fehlen in der Datenauskunft (personExportSections): %s", strings.Join(missing, ", "))
	}
}

func TestPersonDataExportZip(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, vid, _ := seasonFixture(t, h, "")
	_, otherVehicle, _ := seasonFixture(t, h, "")
	for _, vehicleID := range []int64{vid, otherVehicle} {
		if _, err := h.Pool.Exec(ctx, `INSERT INTO vehicle_rate_changes (vehicle_id, effective_from, rate, previous_rate, reason)
			VALUES ($1, '2027-01-01', 65, 60, 'Wertsicherung')`, vehicleID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE persons SET email = 'auskunft@example.invalid' WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO mail_log (recipients, subject, ok, error) VALUES ('Auskunft@example.invalid', 'Rechnung R-1', true, '')`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM mail_log WHERE recipients = 'Auskunft@example.invalid'`)
	})
	if _, err := h.Pool.Exec(ctx, `INSERT INTO vehicle_photos (vehicle_id, filename, content_type, byte_size, data) VALUES ($1, 'vorne.jpg', 'image/jpeg', 3, '\x010203')`, vid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO self_service_tokens (token_hash, person_id, expires_at) VALUES ($1, $2, now() + interval '1 day')`, "geheim-"+strconv.FormatInt(pid, 10), pid); err != nil {
		t.Fatal(err)
	}
	ho := createHandover(t, h, vid, `{"direction":"einlagerung","signer_name":"Pay Integration"}`)

	w := callJSON(h, h.PersonDataExport, http.MethodGet, "/api/persons/x/export", strconv.FormatInt(pid, 10), nil)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = b
	}
	for _, want := range []string{"LIESMICH.txt", "manifest.json", "stammdaten.json", "gefaehrte.json", "mail_protokoll.json",
		"preisaenderungen.json",
		"uebergaben/Uebergabeprotokoll-" + strconv.FormatInt(ho.ID, 10) + ".pdf"} {
		if _, ok := files[want]; !ok {
			t.Errorf("Paket ohne %s", want)
		}
	}
	var rateChanges []struct {
		VehicleID int64   `json:"vehicle_id"`
		Rate      float64 `json:"rate"`
	}
	if err := json.Unmarshal(files["preisaenderungen.json"], &rateChanges); err != nil {
		t.Fatal(err)
	}
	if len(rateChanges) != 1 || rateChanges[0].VehicleID != vid || rateChanges[0].Rate != 65 {
		t.Errorf("exported price history = %+v, want only vehicle %d", rateChanges, vid)
	}
	photo := false
	for name, b := range files {
		if strings.HasPrefix(name, "fotos/") && bytes.Equal(b, []byte{1, 2, 3}) {
			photo = true
		}
		if strings.HasSuffix(name, ".json") && bytes.Contains(b, []byte("geheim-")) {
			t.Errorf("%s enthält den Token-Hash", name)
		}
	}
	if !photo {
		t.Error("Foto des Gefährts fehlt im Paket")
	}
	var mails []exportMail
	_ = json.Unmarshal(files["mail_protokoll.json"], &mails)
	if len(mails) != 1 || mails[0].Subject != "Rechnung R-1" {
		t.Errorf("Mail-Protokoll per Adresse (Groß-/Kleinschreibung egal): %s", files["mail_protokoll.json"])
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE entity = 'person' AND entity_id = $1 AND action = 'export'`, pid).Scan(&n); err != nil || n != 1 {
		t.Errorf("Auskunft muss im Änderungsprotokoll stehen: %d %v", n, err)
	}
}
