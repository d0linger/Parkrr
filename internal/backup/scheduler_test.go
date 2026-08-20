package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFireDue(t *testing.T) {
	now := time.Date(2026, 8, 2, 3, 1, 0, 0, time.UTC)
	yesterday := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	today0259 := time.Date(2026, 8, 2, 2, 59, 0, 0, time.UTC)

	// Off (empty cron) is never due, even with no prior run.
	if fireDue("", nil, now) {
		t.Error("empty cron must never fire")
	}
	// Never run + a real schedule: take an initial backup now.
	if !fireDue("0 3 * * *", nil, now) {
		t.Error("a never-run target should fire an initial backup")
	}
	// Ran yesterday at 03:00, now past 03:00 today -> due.
	last := yesterday
	if !fireDue("0 3 * * *", &last, now) {
		t.Error("daily backup should be due after the scheduled time")
	}
	// Ran yesterday, now still before today's 03:00 -> not due.
	if fireDue("0 3 * * *", &last, today0259) {
		t.Error("daily backup should not be due before the scheduled time")
	}
	// Invalid cron never fires.
	if fireDue("nonsense", nil, now) {
		t.Error("invalid cron must never fire")
	}
}

// Die Zwischendatei eines durchgefallenen Laufs bleibt absichtlich liegen — sonst
// würde ein Image ohne pg_restore jede Nacht einen einwandfreien Dump erzeugen und
// sofort wieder löschen. Genau deshalb muss ihre Anzahl gedeckelt sein: bei einem
// Fehler, der sich jede Nacht wiederholt, wuchs der Bestand vorher über die
// Altersfrist von vierzehn Tagen auf vierzehn vollständige Dumps an.
func TestNurDieZwischendateiDesLaufendenVorgangsBleibt(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("anlegen %s: %v", name, err)
		}
		return p
	}
	alt := write("parkrr-20260801-030000.dump.enc.part")
	mittel := write("parkrr-20260810-030000.dump.enc.part")
	aktuell := write("parkrr-20260820-030000.dump.enc.part")
	// Ein fertiges Archiv darf der Sweep NIE anfassen: es ist der letzte gute Stand.
	fertig := write("parkrr-20260819-030000.dump.enc")

	sweepStaleParts(dir, aktuell)

	for _, weg := range []string{alt, mittel} {
		if _, err := os.Stat(weg); !os.IsNotExist(err) {
			t.Errorf("%s hätte weggeräumt werden müssen (err=%v)", filepath.Base(weg), err)
		}
	}
	if _, err := os.Stat(aktuell); err != nil {
		t.Errorf("die Datei des laufenden Vorgangs wurde gelöscht — der Lauf würde sich "+
			"selbst den Boden wegziehen: %v", err)
	}
	if _, err := os.Stat(fertig); err != nil {
		t.Errorf("ein fertiges Archiv wurde angefasst: %v", err)
	}
}

// Die Zeit darf dabei nicht mitreden: eine gerade erst geschriebene Datei aus einem
// FRÜHEREN Lauf ist trotzdem ein Rest. Vorher entschied allein das Alter, und ein
// jede Nacht scheiternder Lauf hinterließ deshalb vierzehn davon.
func TestFrischeResteFruehererLaeufeVerschwindenTrotzdem(t *testing.T) {
	dir := t.TempDir()
	frisch := filepath.Join(dir, "parkrr-20260820-025900.dump.enc.part")
	if err := os.WriteFile(frisch, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	aktuell := filepath.Join(dir, "parkrr-20260820-030000.dump.enc.part")
	if err := os.WriteFile(aktuell, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sweepStaleParts(dir, aktuell)
	if _, err := os.Stat(frisch); !os.IsNotExist(err) {
		t.Errorf("der Rest des vorigen Laufs ist eine Minute alt und blieb liegen (err=%v) — "+
			"genau so füllte sich das Volume", err)
	}
}
