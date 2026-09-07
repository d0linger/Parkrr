package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func obj(name string, age time.Duration, now time.Time) S3Object {
	return S3Object{Name: name, Modified: now.Add(-age)}
}

// Die Tagesgrenze ist ein BODEN: sie darf nur dazu führen, dass MEHR aufbewahrt
// wird. Ohne sie (0) bleibt das bisherige Verhalten Wort für Wort erhalten —
// darauf beruht die Verhaltensneutralität von Migration 054 (Hundert 09).
func TestPrunableS3WithoutKeepDaysIsUnchanged(t *testing.T) {
	now := time.Now()
	objs := []S3Object{
		obj("parkrr-5", 1*time.Hour, now),
		obj("parkrr-4", 2*time.Hour, now),
		obj("parkrr-3", 3*time.Hour, now),
		obj("parkrr-2", 4*time.Hour, now),
		obj("parkrr-1", 5*time.Hour, now),
	}
	got := prunableS3(objs, Retention{Keep: 2}, now)
	if len(got) != 3 {
		t.Fatalf("ohne Tagesgrenze müssen 3 Objekte wegfallen, waren %d", len(got))
	}
}

// Der Fall, für den die Grenze da ist: fünf Läufe am selben Tag. Mit Keep=2 würde
// die reine Anzahl drei davon sofort löschen und die Historie auf Stunden
// eindampfen. Mit KeepDays=7 bleibt alles liegen, bis es wirklich alt ist.
func TestPrunableS3KeepDaysProtectsAFreshBurst(t *testing.T) {
	now := time.Now()
	objs := []S3Object{
		obj("parkrr-5", 1*time.Hour, now),
		obj("parkrr-4", 2*time.Hour, now),
		obj("parkrr-3", 3*time.Hour, now),
		obj("parkrr-2", 4*time.Hour, now),
		obj("parkrr-1", 5*time.Hour, now),
	}
	if got := prunableS3(objs, Retention{Keep: 2, KeepDays: 7}, now); len(got) != 0 {
		t.Fatalf("nichts ist älter als 7 Tage, es darf nichts wegfallen — es wären %d", len(got))
	}
}

// Wirklich alte Objekte jenseits der Anzahl fallen weiterhin weg; die Grenze darf
// das Aufräumen nicht dauerhaft stilllegen.
func TestPrunableS3RemovesTrulyOldObjects(t *testing.T) {
	now := time.Now()
	objs := []S3Object{
		obj("parkrr-4", 1*time.Hour, now),
		obj("parkrr-3", 2*time.Hour, now),
		obj("parkrr-2", 30*24*time.Hour, now),
		obj("parkrr-1", 60*24*time.Hour, now),
	}
	got := prunableS3(objs, Retention{Keep: 2, KeepDays: 7}, now)
	if len(got) != 2 {
		t.Fatalf("die beiden alten Objekte müssen wegfallen, waren %d", len(got))
	}
	for _, o := range got {
		if o.Name == "parkrr-4" || o.Name == "parkrr-3" {
			t.Errorf("%s ist eines der neuesten zwei und darf nicht wegfallen", o.Name)
		}
	}
}

// Keep=0 heißt "gar nicht aufräumen" — auch mit gesetzter Tagesgrenze.
func TestPrunableS3KeepZeroNeverPrunes(t *testing.T) {
	now := time.Now()
	objs := []S3Object{obj("parkrr-2", 90*24*time.Hour, now), obj("parkrr-1", 99*24*time.Hour, now)}
	if got := prunableS3(objs, Retention{KeepDays: 7}, now); got != nil {
		t.Fatalf("Keep=0 darf nichts löschen, waren %d", len(got))
	}
}

// Dasselbe für das Dateiziel, inklusive der Regel "unlesbare Änderungszeit =
// im Zweifel aufbewahren".
func TestPrunableFilesRespectsKeepDays(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	mk := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		when := now.Add(-age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		return p
	}
	// Aufsteigend sortiert, wie pruneDir sie übergibt (Zeitstempel im Namen).
	files := []string{
		mk("parkrr-2026-01-01.dump.enc", 200*24*time.Hour),
		mk("parkrr-2026-08-01.dump.enc", 30*24*time.Hour),
		mk("parkrr-2026-09-06.dump.enc", 2*time.Hour),
		mk("parkrr-2026-09-07.dump.enc", 1*time.Hour),
	}
	if got := prunableFiles(files, Retention{Keep: 2, KeepDays: 365}, now); len(got) != 0 {
		t.Errorf("mit 365 Tagen Boden darf nichts wegfallen, waren %d", len(got))
	}
	got := prunableFiles(files, Retention{Keep: 2, KeepDays: 60}, now)
	if len(got) != 1 || filepath.Base(got[0]) != "parkrr-2026-01-01.dump.enc" {
		t.Errorf("nur das 200 Tage alte Archiv darf wegfallen, waren %v", got)
	}

	// Eine Datei, die zwischen Glob und Stat verschwindet, gilt als NICHT alt genug.
	if err := os.Remove(files[0]); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := prunableFiles(files, Retention{Keep: 2, KeepDays: 60}, now); len(got) != 0 {
		t.Errorf("eine nicht mehr lesbare Datei darf nicht zum Löschen vorgeschlagen werden, waren %v", got)
	}
}
