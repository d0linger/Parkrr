package backup

import (
	"context"
	"strings"
	"testing"
)

// Ohne eingerichteten Empfänger darf nichts passieren — kein Panik-Absturz im
// Scheduler, nur Stille. nil ist der Normalfall (Hundert 04).
func TestAlertBackupFailureWithoutAlerterIsSilent(t *testing.T) {
	alertBackupFailure(context.Background(), nil, "Volume", "Der Lauf brach ab.", "boom")
}

// Die Nachricht muss ohne Rückfrage lesbar sein: welches Ziel, was passiert ist,
// und die Ursache im Klartext. Ein Alarm, der nur "Fehler" sagt, führt zu einer
// Rückfrage statt zu einer Reaktion.
func TestAlertBackupFailureNamesTargetAndCause(t *testing.T) {
	var subject, body string
	var calls int
	alert := func(_ context.Context, s, b string) { calls++; subject, body = s, b }

	alertBackupFailure(context.Background(), alert, "S3", "Der Lauf in den Bucket sicherung brach ab.", "connection refused")

	if calls != 1 {
		t.Fatalf("erwartet 1 Alarm, waren %d", calls)
	}
	if !strings.Contains(subject, "S3") {
		t.Errorf("der Betreff nennt das Ziel nicht: %q", subject)
	}
	for _, want := range []string{"S3", "sicherung", "connection refused", "Wiederherstellungspunkt"} {
		if !strings.Contains(body, want) {
			t.Errorf("die Nachricht enthält %q nicht:\n%s", want, body)
		}
	}
}

// Ohne Detailtext darf keine leere "Details:"-Zeile stehen bleiben — die sieht
// aus, als sei die Ursache verlorengegangen.
func TestAlertBackupFailureOmitsEmptyDetail(t *testing.T) {
	var body string
	alert := func(_ context.Context, _, b string) { body = b }
	alertBackupFailure(context.Background(), alert, "Volume", "Nicht verifiziert.", "")
	if strings.Contains(body, "Details:") {
		t.Errorf("leere Detailzeile in der Nachricht:\n%s", body)
	}
}
