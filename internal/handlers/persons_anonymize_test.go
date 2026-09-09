package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// mail_log hängt an keinem Fremdschlüssel der Person; anonymisiert wird über die
// Empfängerzeile. Verglichen wurde dabei einmal per ILIKE '%' || $1 || '%', und das
// traf zweierlei zu viel:
//
//   - eine fremde Adresse, in der die eigene als Teilstring steckt, und
//   - die Jokerzeichen % und _, die in einer Adresse syntaktisch erlaubt sind. Eine
//     Person mit der Adresse "%@%" hätte beim Löschen die Empfängerspalte des GANZEN
//     Protokolls geleert — und diese Adresse kann ein Kunde im Portal selbst wünschen.
//
// Der Test hält beide Grenzen fest UND die eigentliche Pflicht: die EIGENE Zeile muss
// anonymisiert werden, auch wenn sie in einer Empfängerliste steht.
func TestMailLogAnonymisierungTrifftGenauDieEigeneAdresse(t *testing.T) {
	h := testHandler(t)
	ctx := t.Context()

	// Eigene Adresse mit Jokerzeichen: erlaubt, und der bisherige Vergleich hätte sie
	// zum Platzhalter gemacht.
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	wildcard := "%@%"
	var pid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name, email) VALUES ('Joker','Integration',$1) RETURNING id`,
		wildcard).Scan(&pid); err != nil {
		t.Fatalf("Person anlegen: %v", err)
	}

	// Drei Protokollzeilen: eine fremde, eine mit der eigenen Adresse in einer Liste,
	// und eine, in der die eigene Adresse nur als Teilstring steckt.
	rows := map[string]string{
		"fremd":      "susan-" + tag + "@example.at",
		"eigen":      "a-" + tag + "@example.at, " + wildcard,
		"teilstueck": "x" + wildcard + "y@example.at",
	}
	ids := map[string]int64{}
	for name, rec := range rows {
		var id int64
		if err := h.Pool.QueryRow(ctx,
			`INSERT INTO mail_log (recipients, subject, ok) VALUES ($1,$2,true) RETURNING id`,
			rec, "Testlauf "+tag).Scan(&id); err != nil {
			t.Fatalf("mail_log %s: %v", name, err)
		}
		ids[name] = id
	}
	t.Cleanup(func() {
		for _, id := range ids {
			_, _ = h.Pool.Exec(context.Background(), `DELETE FROM mail_log WHERE id=$1`, id)
		}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/persons/"+strconv.FormatInt(pid, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	h.DeletePerson(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("Löschen: %d — %s", rec.Code, rec.Body.String())
	}

	want := map[string]bool{"fremd": false, "eigen": true, "teilstueck": false}
	for name, anonymized := range want {
		var got string
		if err := h.Pool.QueryRow(ctx, `SELECT recipients FROM mail_log WHERE id=$1`, ids[name]).Scan(&got); err != nil {
			t.Fatalf("lesen %s: %v", name, err)
		}
		if (got == "anonymisiert") != anonymized {
			if anonymized {
				t.Errorf("%s: die eigene Adresse muss verschwinden, steht aber noch: %q", name, got)
			} else {
				t.Errorf("%s: eine FREMDE Zeile wurde mitgelöscht (%q) — der Vergleich greift zu weit", name, got)
			}
		}
	}
}

// Beide Seiten des Vergleichs muessen getrimmt werden, nicht nur die gespeicherte
// Empfaengerzeile. Der Versandweg schreibt die BEREINIGTE Adresse ins Protokoll
// (reminders.go trimmt vor dem Senden, mail.cleanAddrs parst zusaetzlich nach
// RFC 5322), waehrend persons.email Leerraum tragen kann: portal_requests uebernimmt
// den Kundenwunsch ungetrimmt. Verglichen wurde einmal `lower($1)` gegen
// `lower(trim(x))` — eine Person mit " kunde@x.at" bekam gemeldet, sie sei geloescht,
// waehrend ihre Adresse im Protokoll stehen blieb. Eine verfehlte Loeschung ohne
// Fehlermeldung, und genau die Sorte, die niemand bemerkt.
//
// Der Tabulator gehoert dazu: PostgreSQLs trim() entfernt nur Leerzeichen.
func TestMailLogAnonymisierungTrimmtAuchDieGespeicherteAdresse(t *testing.T) {
	h := testHandler(t)
	ctx := t.Context()

	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	clean := "lueckenhaft-" + tag + "@example.at"
	padded := " 	" + clean + " "

	var pid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name, email) VALUES ('Leerraum','Integration',$1) RETURNING id`,
		padded).Scan(&pid); err != nil {
		t.Fatalf("Person anlegen: %v", err)
	}
	var logID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO mail_log (recipients, subject, ok) VALUES ($1,$2,true) RETURNING id`,
		clean, "Testlauf "+tag).Scan(&logID); err != nil {
		t.Fatalf("mail_log: %v", err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM mail_log WHERE id=$1`, logID) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/persons/"+strconv.FormatInt(pid, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	h.DeletePerson(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("Löschen: %d — %s", rec.Code, rec.Body.String())
	}

	var got string
	if err := h.Pool.QueryRow(ctx, `SELECT recipients FROM mail_log WHERE id=$1`, logID).Scan(&got); err != nil {
		t.Fatalf("lesen: %v", err)
	}
	if got != "anonymisiert" {
		t.Errorf("die Adresse muss auch bei gespeichertem Leerraum verschwinden, steht aber noch: %q", got)
	}
}
