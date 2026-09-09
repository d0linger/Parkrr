package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// changeStatus schickt einen Statuswechsel über den echten Handler.
func changeStatus(t *testing.T, h *Handler, vid int64, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/status", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	rec := httptest.NewRecorder()
	h.ChangeVehicleStatus(rec, req)
	return rec
}

func vehicleRow(t *testing.T, h *Handler, vid int64) (status string, end *time.Time, archived, paid bool) {
	t.Helper()
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT status, end_date, archived, paid FROM vehicles WHERE id=$1`, vid).
		Scan(&status, &end, &archived, &paid); err != nil {
		t.Fatalf("read vehicle: %v", err)
	}
	return
}

// Der Kern des Lebenszyklus (Hundert 92): ein SCHLIESSENDER Status (collected/
// cancelled) MUSS ein Enddatum setzen — sonst läuft die Miete auf einem
// abgeholten Gefährt ewig weiter (Phantom-Forderung). Und die Wiedereröffnung
// (stored) muss es wieder LÖSCHEN, sonst rechnet das aktive Gefährt ab dem alten
// Datum still gar nichts mehr ab.
func TestVehicleCloseSetsEndDateAndReopenClearsIt(t *testing.T) {
	h := testHandler(t)
	vid := mkPhotoVehicle(t, h) // Person+Tarif+Gefährt, Status stored

	// Abholen ohne Datum: Enddatum = heute.
	if rec := changeStatus(t, h, vid, map[string]any{"status": "collected"}); rec.Code != http.StatusOK {
		t.Fatalf("collect: %d %s", rec.Code, rec.Body.String())
	}
	st, end, _, _ := vehicleRow(t, h, vid)
	if st != "collected" {
		t.Fatalf("Status %q", st)
	}
	if end == nil {
		t.Fatal("Abholen ohne Enddatum — die Miete liefe ewig weiter")
	}
	if got := end.Format("2006-01-02"); got != h.now().Format("2006-01-02") {
		t.Errorf("Enddatum %s, erwartet heute", got)
	}

	// Wieder einlagern: das Enddatum MUSS weg, sonst deckelt es die Abgrenzung.
	if rec := changeStatus(t, h, vid, map[string]any{"status": "stored"}); rec.Code != http.StatusOK {
		t.Fatalf("re-store: %d %s", rec.Code, rec.Body.String())
	}
	st, end, _, _ = vehicleRow(t, h, vid)
	if st != "stored" || end != nil {
		t.Errorf("nach Wiedereinlagerung: status=%q end=%v — das alte Enddatum würde die Miete still deckeln", st, end)
	}

	// Rückdatiert abholen.
	back := h.now().AddDate(0, 0, -10).Format("2006-01-02")
	if rec := changeStatus(t, h, vid, map[string]any{"status": "collected", "date": back}); rec.Code != http.StatusOK {
		t.Fatalf("backdate collect: %d %s", rec.Code, rec.Body.String())
	}
	_, end, _, _ = vehicleRow(t, h, vid)
	if end == nil || end.Format("2006-01-02") != back {
		t.Errorf("rückdatiertes Enddatum %v, erwartet %s", end, back)
	}
}

// Stornieren (cancelled) archiviert sofort — es gibt nichts mehr abzurechnen.
// Abholen (collected) archiviert erst, wenn bezahlt ist: vorher wäre die offene
// Forderung in der Ansicht "Archiv" versteckt.
func TestVehicleAutoArchiveRules(t *testing.T) {
	h := testHandler(t)

	cancelled := mkPhotoVehicle(t, h)
	if rec := changeStatus(t, h, cancelled, map[string]any{"status": "cancelled"}); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	if _, _, archived, _ := vehicleRow(t, h, cancelled); !archived {
		t.Error("ein storniertes Gefährt muss automatisch archiviert werden")
	}

	collected := mkPhotoVehicle(t, h)
	if rec := changeStatus(t, h, collected, map[string]any{"status": "collected"}); rec.Code != http.StatusOK {
		t.Fatalf("collect: %d %s", rec.Code, rec.Body.String())
	}
	if _, _, archived, paid := vehicleRow(t, h, collected); archived || paid {
		t.Error("abgeholt aber UNBEZAHLT darf nicht archiviert sein — die Forderung wäre versteckt")
	}
}

// Ein archiviertes Gefährt ist schreibgeschützt: ein weiterer Statuswechsel ist
// ein 409 mit klarer Ansage, kein stilles Nichts und kein 404.
func TestArchivedVehicleRejectsStatusChange(t *testing.T) {
	h := testHandler(t)
	vid := mkPhotoVehicle(t, h)
	if rec := changeStatus(t, h, vid, map[string]any{"status": "cancelled"}); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d", rec.Code)
	}
	if _, _, archived, _ := vehicleRow(t, h, vid); !archived {
		t.Fatal("Vorannahme: cancelled muss archivieren")
	}
	rec := changeStatus(t, h, vid, map[string]any{"status": "stored"})
	if rec.Code != http.StatusConflict {
		t.Errorf("Statuswechsel am Archivierten: %d, erwartet 409 — %s", rec.Code, rec.Body.String())
	}
}

// Ungültiger Status und unbekanntes Gefährt: saubere 400/404, kein 500.
func TestVehicleStatusValidation(t *testing.T) {
	h := testHandler(t)
	vid := mkPhotoVehicle(t, h)
	if rec := changeStatus(t, h, vid, map[string]any{"status": "verschollen"}); rec.Code != http.StatusBadRequest {
		t.Errorf("ungültiger Status: %d, erwartet 400", rec.Code)
	}
	if rec := changeStatus(t, h, vid, map[string]any{"status": "collected", "date": "gestern"}); rec.Code != http.StatusBadRequest {
		t.Errorf("ungültiges Datum: %d, erwartet 400", rec.Code)
	}
	if rec := changeStatus(t, h, 99999999, map[string]any{"status": "collected"}); rec.Code != http.StatusNotFound {
		t.Errorf("unbekanntes Gefährt: %d, erwartet 404", rec.Code)
	}
}

// Jeder Wechsel hinterlässt Geschichte: die Historie muss den Übergang und die
// Notiz tragen — sie ist der Beleg dafür, WANN ein Zustand galt.
func TestVehicleStatusHistoryIsRecorded(t *testing.T) {
	h := testHandler(t)
	vid := mkPhotoVehicle(t, h)
	if rec := changeStatus(t, h, vid, map[string]any{"status": "collected", "note": "Schlüssel übergeben"}); rec.Code != http.StatusOK {
		t.Fatalf("collect: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/history", nil)
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	rec := httptest.NewRecorder()
	h.VehicleHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("history: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"stored", "collected", "Schlüssel übergeben"} {
		if !bytes.Contains([]byte(body), []byte(want)) {
			t.Errorf("Historie ohne %q: %s", want, body)
		}
	}
}
