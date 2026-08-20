package handlers

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// The audit trail must never carry a secret in clear text. These tests pin the
// policy at the choke point (redactChanges), which auditExec applies to every
// write, so a new handler cannot leak by forgetting a skip list.

func redacted(t *testing.T, changes any) map[string]any {
	t.Helper()
	b, err := json.Marshal(redactChanges(normalizeChanges(changes)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func diffOf(field string, oldV, newV any) map[string]any {
	return map[string]any{field: map[string]any{"old": oldV, "new": newV}}
}

func TestRedactSecretsWholesale(t *testing.T) {
	secrets := []string{
		"password", "current_password", "new_password", "password_hash",
		"token", "csrf_token", "api_key", "apikey", "s3_secret_key",
		"totp_secret", "totp_code", "backup_code", "recovery_codes",
		"code", "salt", "access_key", "private_key", "credential",
	}
	for _, f := range secrets {
		got := redacted(t, diffOf(f, "hunter2", "correct horse battery staple"))
		inner := got[f].(map[string]any)
		if inner["old"] != redactedMark || inner["new"] != redactedMark {
			t.Errorf("%s not redacted: old=%v new=%v", f, inner["old"], inner["new"])
		}
	}
}

func TestRedactNeverLeaksTheSecretAnywhereInTheJSON(t *testing.T) {
	// Strongest form: the plaintext must not appear in the serialized row at all.
	const plain = "SuperSecret!42"
	b, err := json.Marshal(redactChanges(normalizeChanges(map[string]any{
		"password":    map[string]any{"old": "", "new": plain},
		"nested":      map[string]any{"s3_secret_key": plain},
		"list":        []any{map[string]any{"token": plain}},
		"seller_name": map[string]any{"old": "A", "new": "B"},
	})))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), plain) {
		t.Fatalf("secret leaked into audit changes: %s", b)
	}
}

func TestRedactBankIdentifiersArePartiallyMasked(t *testing.T) {
	const iban = "AT611904300234573201"
	got := redacted(t, diffOf("iban", iban, "AT611904300234571111"))
	inner := got["iban"].(map[string]any)
	oldS, newS := inner["old"].(string), inner["new"].(string)
	if strings.Contains(oldS, "1904300234") || strings.Contains(newS, "1904300234") {
		t.Fatalf("IBAN body not masked: old=%q new=%q", oldS, newS)
	}
	// The tail must survive so the change stays provable/attributable.
	if !strings.HasSuffix(oldS, "3201") || !strings.HasSuffix(newS, "1111") {
		t.Fatalf("IBAN tail lost, change no longer attributable: old=%q new=%q", oldS, newS)
	}
	// A too-short value cannot be masked meaningfully → redacted wholesale.
	short := redacted(t, diffOf("bic", "AB", "CD"))
	if short["bic"].(map[string]any)["old"] != redactedMark {
		t.Errorf("short BIC should be redacted wholesale, got %v", short["bic"])
	}
}

func TestRedactKeepsOrdinaryBusinessFields(t *testing.T) {
	// An audit trail that redacts everything is useless — normal fields must survive.
	got := redacted(t, map[string]any{
		"name":   map[string]any{"old": "Hermann 1", "new": "Hermann 2"},
		"amount": map[string]any{"old": 100, "new": 250},
		"status": map[string]any{"old": "stored", "new": "collected"},
	})
	if got["name"].(map[string]any)["new"] != "Hermann 2" {
		t.Errorf("name must stay readable, got %v", got["name"])
	}
	if got["amount"].(map[string]any)["new"].(float64) != 250 {
		t.Errorf("amount must stay readable, got %v", got["amount"])
	}
	if got["status"].(map[string]any)["old"] != "stored" {
		t.Errorf("status must stay readable, got %v", got["status"])
	}
}

func TestRedactNilStaysNil(t *testing.T) {
	// "was not set" must remain distinguishable from "was set and hidden".
	got := redacted(t, diffOf("password", nil, "x"))
	inner := got["password"].(map[string]any)
	if inner["old"] != nil {
		t.Errorf("nil old should stay nil, got %v", inner["old"])
	}
	if inner["new"] != redactedMark {
		t.Errorf("set new should be redacted, got %v", inner["new"])
	}
}

func TestRedactStructInput(t *testing.T) {
	// Call sites may pass a struct rather than a diff map; the walk must still apply.
	type payload struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		IBAN     string `json:"iban"`
	}
	got := redacted(t, payload{Name: "Max", Password: "s3cret", IBAN: "AT611904300234573201"})
	if got["password"] != redactedMark {
		t.Errorf("struct password not redacted: %v", got["password"])
	}
	if got["name"] != "Max" {
		t.Errorf("struct name should survive: %v", got["name"])
	}
	if s, _ := got["iban"].(string); strings.Contains(s, "1904300234") {
		t.Errorf("struct IBAN not masked: %v", got["iban"])
	}
}

// maskTail slices by rune: the value is whatever a user typed into an IBAN field, and
// byte slicing there cuts a multibyte character in half (the trail then shows U+FFFD).
func TestMaskTailIsRuneSafe(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"AT611904300234573201", "…3201"},     // the ASCII case must be unchanged
		{"AT61190430023457äöüß", "…äöüß"},     // 4 runes = 8 bytes; byte slicing would split ö
		{"  AT611904300234573201  ", "…3201"}, // trimmed first
		{"abcd", redactedMark},                // exactly keep runes → nothing left to reveal
		{"äöüß", redactedMark},                // 4 runes / 8 bytes: rune count decides, not len()
	} {
		if got := maskTail(tc.in, 4); got != tc.want {
			t.Errorf("maskTail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := maskTail("AT61190430023457äöüß", 4); !utf8.ValidString(got) {
		t.Errorf("maskTail produced invalid UTF-8: %q", got)
	}
}

// Die Einstufung hängt am FELDNAMEN, nicht an der Form seines Wertes. Ein Secret-Feld,
// dessen Wert eine verschachtelte Map mit anderen Schlüsseln als old/new ist, wurde
// vorher nach dem INNEREN Schlüssel beurteilt — der auf keine Regel passt — und kam
// im Klartext durch. Kein heutiger Aufrufer erzeugt diese Form; die Zusage im
// Dateikopf gilt aber nur, wenn sie formunabhängig hält.
func TestSecretFeldnameGiltUnabhaengigVonDerWertform(t *testing.T) {
	const secret = "sk-live-4f9c2b7a-DIESER-WERT-DARF-NIE-AUFTAUCHEN"
	for _, tc := range []struct {
		name string
		in   any
		want any // das GANZE erwartete Ergebnis, nicht nur "irgendwo maskiert"
	}{
		{"verschachtelte Map unter einem Secret-Feld",
			map[string]any{"api_key": map[string]any{"prod": secret}},
			map[string]any{"api_key": map[string]any{"prod": redactedMark}}},
		{"mehrere Ebenen",
			map[string]any{"password": map[string]any{"env": map[string]any{"live": secret}}},
			// Die ganze Untermap wird ersetzt, nicht nur ihr Blatt: unter einem
			// Secret-Feld ist auch die Struktur nichts, was der Trail braucht.
			map[string]any{"password": map[string]any{"env": redactedMark}}},
		{"gemischt: old/new plus ein Fremdschlüssel",
			map[string]any{"totp_secret": map[string]any{"old": nil, "new": secret, "quelle": secret}},
			// old bleibt null. "Es war vorher nichts gesetzt" ist eine andere Aussage
			// als "hier stand ein Wert" — ein Maskierungszeichen an dieser Stelle würde
			// eine Änderung behaupten, die es nicht gab.
			map[string]any{"totp_secret": map[string]any{"old": nil, "new": redactedMark, "quelle": redactedMark}}},
		{"Liste unter einem Secret-Feld",
			map[string]any{"backup_code": map[string]any{"werte": []any{secret, secret}}},
			map[string]any{"backup_code": map[string]any{"werte": redactedMark}}},
	} {
		checkRedacted(t, tc.name, tc.in, tc.want, secret)
	}
}

// Bankkennungen laufen über denselben Zweig, werden aber nur TEILWEISE maskiert. Die
// Einstufung muss auch hier am äußeren Feldnamen hängen: unter einem fremden inneren
// Schlüssel wie "prod" griffe sonst keine Regel und die volle IBAN stünde im Klartext
// im Trail. Der Rest der Kennung darf bleiben — er beweist, um welches Konto es ging.
func TestBankfeldnameGiltUnabhaengigVonDerWertform(t *testing.T) {
	const iban = "AT611904300234573201"
	const bic = "GIBAATWWXXX"
	// Der erwartete Wert steht ausgeschrieben da, samt sichtbarem Schluss: nur so
	// prüft der Test BEIDE Hälften der Zusage — die Kennung verschwindet, und der
	// Rest bleibt, sonst sagt der Eintrag nicht mehr, welches Konto gemeint war.
	for _, tc := range []struct {
		name string
		in   any
		want any
		voll string
	}{
		{"IBAN unter einem fremden Schlüssel",
			map[string]any{"iban": map[string]any{"prod": iban}},
			map[string]any{"iban": map[string]any{"prod": "…3201"}}, iban},
		{"BIC unter einem fremden Schlüssel",
			map[string]any{"bic": map[string]any{"prod": bic}},
			map[string]any{"bic": map[string]any{"prod": "…WXXX"}}, bic},
		{"IBAN, mehrere Ebenen tief",
			map[string]any{"iban": map[string]any{"env": map[string]any{"live": iban}}},
			// Keine Zeichenkette, also kein Teil-Maskieren möglich: hier fällt die
			// ganze Untermap weg statt einen Schluss übrig zu lassen.
			map[string]any{"iban": map[string]any{"env": redactedMark}}, iban},
		{"gemischt: old/new plus ein Fremdschlüssel",
			map[string]any{"iban": map[string]any{"old": nil, "new": iban, "quelle": iban}},
			map[string]any{"iban": map[string]any{"old": nil, "new": "…3201", "quelle": "…3201"}}, iban},
		{"Liste unter einem Bankfeld",
			map[string]any{"iban": map[string]any{"werte": []any{iban, iban}}},
			map[string]any{"iban": map[string]any{"werte": redactedMark}}, iban},
	} {
		checkRedacted(t, tc.name, tc.in, tc.want, tc.voll)
	}
}

// checkRedacted redigiert in und belegt zweierlei.
//
// Erstens: der vollständige Wert taucht nirgends mehr auf — die eigentliche Zusage.
//
// Zweitens: das Ergebnis sieht GENAU wie want aus. Vorher stand hier nur "irgendwo im
// Ergebnis steht ein Maskierungszeichen". Das kann nicht sehen, WO maskiert wurde:
// ein Ergebnis, das ein maskiertes Feld richtig behandelt und daneben etwas anderes
// verliert oder erfindet, käme durch, solange nur der volle Wert weg ist. Genau das
// unterscheidet "richtig maskiert" von "zufällig nicht gefunden".
//
// Verglichen wird über normalizeChanges, damit beide Seiten dieselbe JSON-Gestalt
// haben (map[string]any, nil bleibt nil) und die Schlüsselreihenfolge nichts entscheidet.
func checkRedacted(t *testing.T, name string, in, want any, voll string) {
	t.Helper()
	blob, err := json.Marshal(redactChanges(normalizeChanges(in)))
	if err != nil {
		t.Fatalf("%s: nicht serialisierbar: %v", name, err)
	}
	s := string(blob)
	if strings.Contains(s, voll) {
		t.Errorf("%s: der vollständige Wert steht im Klartext im Ergebnis:\n%s", name, s)
	}
	var got any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("%s: Ergebnis nicht lesbar: %v", name, err)
	}
	if soll := normalizeChanges(want); !reflect.DeepEqual(got, soll) {
		sb, _ := json.Marshal(soll)
		t.Errorf("%s: das Ergebnis weicht ab\n  ist:  %s\n  soll: %s", name, s, sb)
	}
}

// Die Gegenprobe: ein harmloses Feld mit derselben Wertform darf NICHT maskiert
// werden, sonst wäre der Trail leer statt sicher. Verglichen wird die GANZE Map:
// "kein Maskierungszeichen und ein Wert stimmt" ließe offen, ob der zweite Wert
// unterwegs verlorengegangen ist — ein stiller Datenverlust im Trail sähe genauso
// aus wie ein sauberer Durchlauf.
func TestGewoehnlichesFeldMitVerschachtelterMapBleibtLesbar(t *testing.T) {
	in := map[string]any{"adresse": map[string]any{"strasse": "Hauptplatz 1", "ort": "Graz"}}
	got := redactChanges(normalizeChanges(in))
	if want := normalizeChanges(in); !reflect.DeepEqual(got, want) {
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		t.Errorf("ein gewöhnliches Feld muss unverändert durchgehen\n  ist:  %s\n  soll: %s", gb, wb)
	}
}
