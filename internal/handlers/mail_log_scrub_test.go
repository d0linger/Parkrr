package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// PRT-03: die Löschung erreicht JEDE Adresse der Person im Versandprotokoll — auch
// eine frühere (hier über das Audit-Protokoll eines E-Mail-Wechsels bekannt) und
// auch im Fehlertext, in dem das Relay die abgewiesene Adresse zitiert. Eine
// fremde Adresse, in der die eigene nur als Teilstring steckt, bleibt unberührt.
func TestAnonymizeScrubsAllKnownAddressesFromMailLog(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	id := seedPerson(t, h) // heutige Adresse: hermann@example.at

	const oldAddr = "alt.hermann@example.at"
	h.auditChange(httptest.NewRequest(http.MethodPut, "/", nil), "update", "person", id, "Adresse geändert",
		diffFields(map[string]any{"email": oldAddr}, map[string]any{"email": "hermann@example.at"}))

	const subj = "PRT03-Scrub"
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO mail_log (recipients, subject, ok, error) VALUES
		   ($1, $4, false, 'mail: RCPT alt.hermann@example.at: 550 5.1.1 <ALT.Hermann@Example.at>: rejected'),
		   ($2, $4, false, 'mail: RCPT hermann@example.at: 452 mailbox full'),
		   ($3, $4, false, 'mail: RCPT xalt.hermann@example.at: 550 unknown')`,
		oldAddr, "hermann@example.at", "xalt.hermann@example.at", subj); err != nil {
		t.Fatalf("Versandprotokoll: %v", err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM mail_log WHERE subject=$1`, subj) })

	if rec := anonymize(t, h, id); rec.Code != http.StatusOK {
		t.Fatalf("anonymisieren: %d %s", rec.Code, rec.Body.String())
	}

	rows, err := h.Pool.Query(ctx, `SELECT recipients, error FROM mail_log WHERE subject=$1 ORDER BY id`, subj)
	if err != nil {
		t.Fatalf("lesen: %v", err)
	}
	defer rows.Close()
	var got [][2]string
	for rows.Next() {
		var rc, e string
		if err := rows.Scan(&rc, &e); err != nil {
			t.Fatal(err)
		}
		got = append(got, [2]string{rc, e})
	}
	if len(got) != 3 {
		t.Fatalf("%d Zeilen, erwartet 3", len(got))
	}
	for i, row := range got[:2] {
		if row[0] != "anonymisiert" {
			t.Errorf("Zeile %d: Empfänger nicht geschwärzt: %q", i, row[0])
		}
		lower := strings.ToLower(row[1])
		if strings.Contains(lower, "hermann@example.at") {
			t.Errorf("Zeile %d: Adresse steht noch im Fehlertext: %q", i, row[1])
		}
	}
	if !strings.Contains(got[0][1], "550 5.1.1") {
		t.Errorf("der SMTP-Status ist Diagnose und muss bleiben: %q", got[0][1])
	}
	if got[2][0] != "xalt.hermann@example.at" || !strings.Contains(got[2][1], "xalt.hermann@example.at") {
		t.Errorf("eine fremde Adresse wurde mitgeschwärzt: %+v", got[2])
	}
}
