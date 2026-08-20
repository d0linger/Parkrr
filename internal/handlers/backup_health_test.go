package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/backup"
)

// The header dot is the one backup signal an editor ever sees, so its mapping has to
// be right in both directions: nothing may look green that is not, and nothing may
// look broken that merely needs setting up.

func TestBackupToneSeparatesBrokenFromUnconfigured(t *testing.T) {
	// "bad" means something tried and failed — repair. Everything else that is not
	// ok means something needs arranging. Collapsing the two would make the color
	// stop telling the operator which of the two actions applies.
	for state, want := range map[string]string{
		"ok":     "ok",
		"failed": "bad",
		"stale":  "warn",
		"never":  "warn",
		"off":    "warn",
		"":       "warn", // unknown must never read as healthy
	} {
		if got := backupToneOf(state); got != want {
			t.Errorf("state %q -> tone %q, want %q", state, got, want)
		}
	}
}

func TestWorstStateWins(t *testing.T) {
	// A healthy volume target must not mask a dead S3 — the dot shows one color, so
	// it has to be the worse one.
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"ok", "ok"}, "ok"},
		{[]string{"ok", "failed"}, "failed"},
		{[]string{"failed", "ok"}, "failed"},
		{[]string{"ok", "stale"}, "stale"},
		{[]string{"stale", "never"}, "never"},
		{[]string{"never", "failed"}, "failed"},
		{nil, "ok"},
	} {
		if got := worstBackupState(tc.in); got != tc.want {
			t.Errorf("worst(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBackupStateOfClassifiesOneTarget(t *testing.T) {
	past := time.Now().Add(-3 * time.Hour)
	for _, tc := range []struct {
		name string
		th   backup.TargetHealth
		want string
	}{
		{"nicht eingerichtet", backup.TargetHealth{Configured: false}, "off"},
		{"letzter Lauf fehlgeschlagen", backup.TargetHealth{Configured: true, LastOK: false}, "failed"},
		{"nie gelaufen", backup.TargetHealth{Configured: true, LastOK: true, Never: true}, "never"},
		{"überfällig", backup.TargetHealth{Configured: true, LastOK: true, Overdue: true}, "stale"},
		{"gesund", backup.TargetHealth{Configured: true, LastOK: true, LastAt: &past}, "ok"},
	} {
		if got := backupStateOf(tc.th); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	// failed schlägt never: ein gescheiterter Versuch ist die konkretere Aussage.
	both := backup.TargetHealth{Configured: true, LastOK: false, Never: true}
	if got := backupStateOf(both); got != "failed" {
		t.Errorf("fehlgeschlagen UND nie gelaufen -> %q, want failed", got)
	}
}

func TestHumanAgeDEReadsAtAGlance(t *testing.T) {
	// Der Punkt hat Platz für einen Blick, nicht für einen Zeitstempel.
	for _, tc := range []struct {
		sec  int64
		want string
	}{
		{30, "gerade eben"},
		{119, "gerade eben"},
		{120, "vor 2 Min."},
		{59 * 60, "vor 59 Min."},
		{3600, "vor 1 Std."},
		{2 * 3600, "vor 2 Std."},
		{23 * 3600, "vor 23 Std."},
		{24 * 3600, "vor 1 Tag"},
		{47 * 3600, "vor 1 Tag"},
		{48 * 3600, "vor 2 Tagen"},
		{10 * 24 * 3600, "vor 10 Tagen"},
	} {
		if got := humanAgeDE(tc.sec); got != tc.want {
			t.Errorf("humanAgeDE(%d) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}

// Ohne Backup-Schlüssel gibt es nichts zu überwachen — das muss als "einrichten"
// erscheinen und nicht als Störung, sonst gewöhnt man sich das Rot an.
func TestHealthWithoutKeyIsAWarningNotAnError(t *testing.T) {
	h := &Handler{} // kein BackupKey, keine DB nötig für diesen Zweig
	w := httptest.NewRecorder()
	h.BackupHealth(w, httptest.NewRequest(http.MethodGet, "/api/backup/health", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("Status %d, want 200 — die Ampel darf nie einen Fehler liefern", w.Code)
	}
	var got backupHealthView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("Antwort nicht lesbar: %v", err)
	}
	if got.Tone != "warn" || got.Title != "Backups nicht aktiviert" {
		t.Errorf("tone=%q title=%q, want warn / Backups nicht aktiviert", got.Tone, got.Title)
	}
	if got.AgeLabel != "" {
		t.Errorf("ohne Backup gibt es kein Alter, got %q", got.AgeLabel)
	}
}
