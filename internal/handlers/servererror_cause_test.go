package handlers

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// serverError stasht die Ursache im Request-Kontext, damit der Zugriffslogger sie
// an die 500er-Zeile hängt. Das funktioniert nur, wenn der Aufrufer auch die
// Variable übergibt, die der geprüfte Fehler ist.
//
// Genau das ging bei elf Stellen schief: der if-Block bindet eine eigene Variable
// (serr, perr, cerr, ierr …), übergeben wurde aber die äußere `err` — die dort
// nachweislich nil ist, weil ihr letzter Test erfolgreich war. Ergebnis: ein 500
// ohne jede Ursache im Protokoll, also exakt der Zustand, den die zentrale
// Fehlersichtbarkeit beseitigen sollte. Der Compiler sieht das nicht (beide
// Variablen sind gültige error-Werte), `go vet` auch nicht.
//
// Der Test liest die Quelle: für jeden serverError-Aufruf sucht er den nächsten
// darüberliegenden Fehlertest und vergleicht die Namen.
var (
	reServerErrCall = regexp.MustCompile(`serverError\(w, r, [^,]+, (\w+)\)`)
	// "if x != nil {" und "if x := f(); x != nil {" — in beiden Formen ist der
	// Name vor dem "!= nil" der geprüfte Fehler.
	reNilCheck = regexp.MustCompile(`\bif\s+(?:(\w+)\s*:?=[^;]*;\s*)?(\w+)\s*!=\s*nil\s*\{`)
)

func TestServerErrorLogsTheCheckedError(t *testing.T) {
	var bad []string
	for _, hf := range handlerFuncs(t) {
		lines := strings.Split(hf.Body, "\n")
		for i, ln := range lines {
			m := reServerErrCall.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			passed := m[1]
			// Den nächsten Fehlertest oberhalb suchen. Fünf Zeilen reichen: dazwischen
			// liegen höchstens ein rows.Close() und ein Kommentar.
			var checked string
			for j := i; j >= 0 && j > i-5; j-- {
				if c := reNilCheck.FindStringSubmatch(lines[j]); c != nil {
					checked = c[2]
					break
				}
			}
			if checked == "" || checked == passed {
				continue
			}
			bad = append(bad, hf.File+" "+hf.Name+": geprüft wird "+checked+", protokolliert wird "+passed)
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("serverError bekommt eine ANDERE Variable als die geprüfte — die Ursache des 500 landet\n"+
			"damit nicht im Protokoll (der übergebene Wert ist an dieser Stelle nil):\n  %s",
			strings.Join(bad, "\n  "))
	}
}
