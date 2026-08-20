package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Anonymisieren nach DSGVO Art. 17. Der Kern ist nicht, dass die Personendaten
// verschwinden — das ist ein UPDATE. Der Kern ist, dass dabei GENAU das erhalten
// bleibt, was erhalten bleiben muss (der Beleg), und dass der Vorgang nicht durch
// eine Hintertür rückgängig gemacht werden kann.
//
// Laufen nur mit PARKRR_TEST_DATABASE_URL (testHandler überspringt sonst).

// seedPerson legt eine Person mit vollständigen Personendaten an.
func seedPerson(t *testing.T, h *Handler) int64 {
	t.Helper()
	var id int64
	err := h.Pool.QueryRow(context.Background(),
		`INSERT INTO persons (first_name, last_name, email, phone, address, notes)
		 VALUES ('Hermann','Mayer','hermann@example.at','+43 660 1234','Hauptplatz 1','Stammkunde')
		 RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("Person anlegen: %v", err)
	}
	return id
}

func anonymize(t *testing.T, h *Handler, id int64) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(id, 10)+"/anonymize", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	h.AnonymizePerson(w, req)
	return w
}

// Der wichtigste Test: die eingefrorene Rechnung behält Name und Adresse. Sie zu
// überschreiben hieße, ein aufbewahrungspflichtiges Dokument zu verfälschen —
// Art. 17 Abs. 3 lit. b DSGVO nimmt solche Pflichten von der Löschung aus.
func TestAnonymisierenLaesstDenRechnungsbelegUnberuehrt(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := seedPerson(t, h)

	buyer := `{"name":"Hermann Mayer","address":"Hauptplatz 1"}`
	// Nummer pro Lauf eindeutig: invoices.number ist UNIQUE, und Rechnungen sind
	// unveränderlich — das Cleanup zwischen zwei Testläufen räumt sie nicht weg.
	// Eine feste Nummer ließ den zweiten Lauf gegen dieselbe Datenbank scheitern.
	number := "ANON-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	var invID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO invoices (number, person_id, issued_on, subtotal, ust_rate, tax_amount, total,
		        kleinunternehmer, seller_snapshot, buyer_snapshot, created_by)
		 VALUES ($3,$1, now(), 100, 20, 20, 120, false, '{}', $2, NULL) RETURNING id`,
		pid, buyer, number).Scan(&invID); err != nil {
		t.Fatalf("Rechnung anlegen: %v", err)
	}

	if w := anonymize(t, h, pid); w.Code != http.StatusOK {
		t.Fatalf("Status %d, want 200: %s", w.Code, w.Body.String())
	}

	// Stammsatz ist geleert …
	var first, last, email, phone, addr, notes string
	var anon bool
	if err := h.Pool.QueryRow(ctx,
		`SELECT first_name, last_name, email, phone, address, notes, anonymized
		   FROM persons WHERE id=$1`, pid).
		Scan(&first, &last, &email, &phone, &addr, &notes, &anon); err != nil {
		t.Fatalf("Person lesen: %v", err)
	}
	for name, got := range map[string]string{"email": email, "phone": phone, "address": addr, "notes": notes} {
		if got != "" {
			t.Errorf("%s wurde nicht geleert: %q", name, got)
		}
	}
	if first != "Anonymisiert" || strings.Contains(last, "Mayer") {
		t.Errorf("Name nicht ersetzt: %q %q", first, last)
	}
	if !anon {
		t.Error("anonymized-Flag wurde nicht gesetzt")
	}

	// … der Beleg NICHT.
	var buyerAfter string
	if err := h.Pool.QueryRow(ctx,
		`SELECT buyer_snapshot::text FROM invoices WHERE id=$1`, invID).Scan(&buyerAfter); err != nil {
		t.Fatalf("Rechnung lesen: %v", err)
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(buyerAfter), &b); err != nil {
		t.Fatalf("Snapshot nicht lesbar: %v", err)
	}
	if b["name"] != "Hermann Mayer" || b["address"] != "Hauptplatz 1" {
		t.Errorf("der eingefrorene Rechnungs-Snapshot wurde verändert: %s", buyerAfter)
	}
}

// Der serverseitige Riegel: ein gebastelter PUT darf gelöschte Daten nicht
// wiederbeleben. Die Oberfläche blendet das Formular aus — darauf allein darf man
// sich nicht verlassen.
func TestAnonymisiertePersonLaesstSichNichtWiederbefuellen(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := seedPerson(t, h)
	if w := anonymize(t, h, pid); w.Code != http.StatusOK {
		t.Fatalf("Anonymisieren fehlgeschlagen: %d %s", w.Code, w.Body.String())
	}

	// Exakt das Statement, das UpdatePerson absetzt.
	ct, err := h.Pool.Exec(ctx,
		`UPDATE persons SET first_name='Hermann', last_name='Mayer', email='hermann@example.at',
		        phone='+43 660 1234', address='Hauptplatz 1', notes='Stammkunde', updated_at=now()
		  WHERE id=$1 AND NOT anonymized`, pid)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if ct.RowsAffected() != 0 {
		t.Error("das WHERE NOT anonymized hat nicht gegriffen — gelöschte Daten wären wiederhergestellt")
	}
	var email string
	if err := h.Pool.QueryRow(ctx, `SELECT email FROM persons WHERE id=$1`, pid).Scan(&email); err != nil {
		t.Fatalf("Person lesen: %v", err)
	}
	if email != "" {
		t.Errorf("die E-Mail ist zurückgekehrt: %q", email)
	}
}

// Zweimal anonymisieren darf nicht erneut "erfolgreich" melden: der Aufrufer soll
// den Unterschied zwischen "gerade erledigt" und "war schon" sehen.
func TestZweitesAnonymisierenMeldetKonflikt(t *testing.T) {
	h := testHandler(t)
	pid := seedPerson(t, h)
	if w := anonymize(t, h, pid); w.Code != http.StatusOK {
		t.Fatalf("erster Aufruf: %d %s", w.Code, w.Body.String())
	}
	if w := anonymize(t, h, pid); w.Code != http.StatusConflict {
		t.Errorf("zweiter Aufruf: %d, want 409 (%s)", w.Code, w.Body.String())
	}
}

// Der Audit-Eintrag darf die gelöschten Werte NICHT enthalten — sonst hebt der
// Trail die Löschung auf: audit_log ist append-only und wird Jahre aufbewahrt.
// (Treckrr schreibt an dieser Stelle den alten Namen mit; hier bewusst nicht.)
func TestAuditEintragEnthaeltDieGeloeschtenDatenNicht(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := seedPerson(t, h)
	if w := anonymize(t, h, pid); w.Code != http.StatusOK {
		t.Fatalf("Anonymisieren fehlgeschlagen: %d %s", w.Code, w.Body.String())
	}

	rows, err := h.Pool.Query(ctx,
		`SELECT summary, COALESCE(changes::text,'') FROM audit_log
		  WHERE action='anonymize' AND entity='person' AND entity_id=$1`, pid)
	if err != nil {
		t.Fatalf("Audit lesen: %v", err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		var summary, changes string
		if err := rows.Scan(&summary, &changes); err != nil {
			t.Fatalf("scan: %v", err)
		}
		n++
		for _, leaked := range []string{"Hermann", "Mayer", "hermann@example.at", "+43 660 1234", "Hauptplatz 1"} {
			if strings.Contains(summary, leaked) || strings.Contains(changes, leaked) {
				t.Errorf("der Audit-Eintrag enthält den gelöschten Wert %q:\n  summary=%s\n  changes=%s",
					leaked, summary, changes)
			}
		}
	}
	if n == 0 {
		t.Error("kein Audit-Eintrag geschrieben — der Vorgang selbst muss belegt sein")
	}
}
