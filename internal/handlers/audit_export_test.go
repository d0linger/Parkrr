package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Der revisionssichere Export (Hundert 43): jede Zeile trägt eine SHA-256-Kette
// über alle vorigen. Der Test spielt den PRÜFER: er rechnet die Kette mit nichts
// als der veröffentlichten Regel nach — und weist dann nach, dass eine manipulierte
// Zeile auffliegt.
func TestAuditExportChainVerifies(t *testing.T) {
	h := testHandler(t)
	// Mindestens zwei frische Einträge, damit die Kette etwas zu binden hat.
	h.auditCreated(httptest.NewRequest(http.MethodGet, "/", nil), "person", 1, "Export-Testeintrag A", nil)
	h.auditCreated(httptest.NewRequest(http.MethodGet, "/", nil), "person", 2, "Export-Testeintrag B", nil)

	rec := httptest.NewRecorder()
	h.ExportAudit(rec, httptest.NewRequest(http.MethodGet, "/api/audit/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}

	verify := func(data []byte) (rows int64, final string, ok bool, reason string) {
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		chain := ""
		var manifest *auditExportManifest
		for sc.Scan() {
			line := sc.Bytes()
			var probe struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(line, &probe); err != nil {
				return rows, chain, false, "kaputte Zeile: " + err.Error()
			}
			if probe.Type == "manifest" {
				manifest = &auditExportManifest{}
				_ = json.Unmarshal(line, manifest)
				continue
			}
			var row auditExportRow
			if err := json.Unmarshal(line, &row); err != nil {
				return rows, chain, false, "kaputter Eintrag: " + err.Error()
			}
			claimed := row.Chain
			row.Chain = ""
			bare, _ := json.Marshal(row)
			chain = auditChainStep(chain, bare)
			if chain != claimed {
				return rows, chain, false, "Kettenbruch bei id"
			}
			rows++
		}
		if manifest == nil {
			return rows, chain, false, "Manifest fehlt (unvollständiger Export)"
		}
		if manifest.Rows != rows || manifest.FinalChain != chain {
			return rows, chain, false, "Manifest passt nicht zur Kette"
		}
		return rows, chain, true, ""
	}

	data := rec.Body.Bytes()
	rows, _, ok, reason := verify(data)
	if !ok {
		t.Fatalf("der unveränderte Export muss prüfbar sein: %s", reason)
	}
	if rows < 2 {
		t.Fatalf("zu wenige Zeilen im Export: %d", rows)
	}

	// Manipulation: EIN Zeichen in einer Zusammenfassung ändern. Genau das muss
	// der Prüfer sehen — sonst ist die Kette Dekoration.
	tampered := bytes.Replace(data, []byte("Export-Testeintrag A"), []byte("Export-Testeintrag X"), 1)
	if bytes.Equal(tampered, data) {
		t.Fatal("Vorannahme: der Testeintrag muss im Export stehen")
	}
	if _, _, ok, _ := verify(tampered); ok {
		t.Error("eine manipulierte Zeile hat die Kettenprüfung überstanden")
	}

	// Und eine ENTFERNTE Zeile ebenso (Löschen ist die naheliegendste Fälschung).
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > 2 {
		dropped := strings.Join(append(lines[:1], lines[2:]...), "\n") + "\n"
		if _, _, ok, _ := verify([]byte(dropped)); ok {
			t.Error("eine entfernte Zeile hat die Kettenprüfung überstanden")
		}
	}
}
