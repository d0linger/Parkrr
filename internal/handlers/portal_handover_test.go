package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// Das Portal zeigt die Übergabeprotokolle der EIGENEN Gefährte (Hundert 84) —
// Richtung, Datum, Notizen, Unterzeichner. Zwei harte Regeln: das
// Unterschriftsbild bleibt draußen (Bearer-Link!), und fremde Protokolle
// erscheinen nicht.
func TestPortalSummaryListsOwnHandoversWithoutSignature(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	pid, proto := mkSignedHandover(t, h) // Person + Gefährt + unterschriebenes Protokoll
	_ = proto
	otherPid, _ := mkSignedHandover(t, h) // ein ZWEITER Kunde mit eigenem Protokoll
	_ = otherPid

	token := createPortalLink(t, h, pid)
	w := getPortalSummary(t, h, token)
	if w.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", w.Code, w.Body.String())
	}
	var sum struct {
		Handovers []struct {
			VehicleLabel string `json:"vehicle_label"`
			Direction    string `json:"direction"`
			Notes        string `json:"notes"`
			SignerName   string `json:"signer_name"`
		} `json:"handovers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sum.Handovers) != 1 {
		t.Fatalf("%d Protokolle im Portal, erwartet genau das eigene", len(sum.Handovers))
	}
	ho := sum.Handovers[0]
	if ho.Direction != "einlagerung" || ho.Notes != "Kratzer links vorne" || ho.SignerName != "Erika Mustermann" {
		t.Errorf("Protokoll-Inhalt falsch: %+v", ho)
	}
	// Das Unterschriftsbild darf in der GESAMTEN Antwort nicht vorkommen.
	if body := w.Body.String(); jsonContains(body, "signature") {
		t.Error("die Portal-Antwort trägt ein signature-Feld — das Bild gehört nicht hinter einen Bearer-Link")
	}
	_ = ctx
}

// jsonContains prüft auf einen Schlüsselnamen (nicht bloß den Substring in einem
// Wert): "signature" als Feldname wäre der Fehler.
func jsonContains(body, key string) bool {
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return false
	}
	var walk func(any) bool
	walk = func(x any) bool {
		switch t := x.(type) {
		case map[string]any:
			for k, vv := range t {
				if k == key || walk(vv) {
					return true
				}
			}
		case []any:
			for _, vv := range t {
				if walk(vv) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}
