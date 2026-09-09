package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// baseEnv setzt das Minimum, das Load verlangt, damit die Zonenprüfung nicht an
// einem fehlenden Passwort scheitert.
func baseEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PARKRR_DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("PARKRR_ADMIN_PASSWORD", "ein-langes-testpasswort")
	t.Setenv("PARKRR_SESSION_SECRET", strings.Repeat("s", 48))
}

// Ohne PARKRR_TIMEZONE bleibt alles wie bisher: die Zone ist time.Local, also
// das, was TZ gesetzt hat, sonst UTC. Das ist die Verhaltensneutralität, auf der
// die ganze Aenderung beruht (Hundert 13).
func TestTimeZoneDefaultsToProcessLocal(t *testing.T) {
	baseEnv(t)
	os.Unsetenv("PARKRR_TIMEZONE")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Location != time.Local {
		t.Errorf("ohne PARKRR_TIMEZONE muss die Zone time.Local sein, war %v", cfg.Location)
	}
}

// Ein gültiger Zonenname wird geladen — und Load darf die Prozesszone dabei NICHT
// selbst umstellen: der Seiteneffekt gehört nach main, sonst verschiebt schon das
// Einlesen der Konfiguration die Kalendergrenzen.
func TestTimeZoneParsedButNotAppliedByLoad(t *testing.T) {
	baseEnv(t)
	before := time.Local
	t.Setenv("PARKRR_TIMEZONE", "Europe/Vienna")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Location == nil || cfg.Location.String() != "Europe/Vienna" {
		t.Fatalf("Zone nicht übernommen: %v", cfg.Location)
	}
	if time.Local != before {
		t.Error("Load hat die Prozesszone verändert — der Seiteneffekt gehört nach main")
	}
}

// Ein Tippfehler im Zonennamen muss den Start ABBRECHEN. Ein stiller Rückfall auf
// UTC würde die Kalendergrenzen um bis zu zwei Stunden verschieben, und das fällt
// erst beim Jahresabschluss auf.
func TestUnknownTimeZoneIsRejected(t *testing.T) {
	baseEnv(t)
	t.Setenv("PARKRR_TIMEZONE", "Europe/Wien")
	if _, err := Load(); err == nil {
		t.Fatal("ein unbekannter Zonenname muss abgewiesen werden")
	} else if !strings.Contains(err.Error(), "PARKRR_TIMEZONE") {
		t.Errorf("die Meldung muss die Variable nennen: %v", err)
	}
}

// Der eigentliche Fehler, den die Einstellung verhindert: um 00:30 Wiener Zeit ist
// es in UTC noch gestern. "Heute" muss der Kalendertag des BETRIEBS sein, nicht der
// des Containers.
func TestCalendarDayFollowsTheBusinessZone(t *testing.T) {
	vienna, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Skipf("Zonendatenbank nicht verfügbar: %v", err)
	}
	instant := time.Date(2026, 9, 6, 23, 30, 0, 0, time.UTC) // = 7.9. 01:30 in Wien
	if got := instant.In(time.UTC).Format("2006-01-02"); got != "2026-09-06" {
		t.Fatalf("Vorannahme falsch: in UTC ist es %s", got)
	}
	if got := instant.In(vienna).Format("2006-01-02"); got != "2026-09-07" {
		t.Errorf("in der Geschäftszone muss es der 7. sein, war %s", got)
	}
}

// Passkey-only ohne WebAuthn wäre eine Installation ohne einen einzigen
// Anmeldeweg — der Start muss das ABLEHNEN, nicht später der erste Login-Versuch.
func TestPasskeyOnlyRequiresWebAuthn(t *testing.T) {
	baseEnv(t)
	t.Setenv("PARKRR_PASSKEY_ONLY", "true")
	os.Unsetenv("PARKRR_WEBAUTHN_RP_ID")
	if _, err := Load(); err == nil {
		t.Fatal("Passkey-only ohne RP-ID muss den Start verweigern")
	}
	t.Setenv("PARKRR_WEBAUTHN_RP_ID", "parkrr.example.com")
	if _, err := Load(); err != nil {
		t.Fatalf("mit RP-ID muss der Start gelingen: %v", err)
	}
}
