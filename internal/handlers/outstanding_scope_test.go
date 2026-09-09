package handlers

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// outstandingByPerson ist der Pfad, den auch das öffentliche Kundenportal nimmt.
// Er war nur halb gescopet: Gefährte, Vereinbarungen und Einzelposten wurden
// gefiltert, die WIEDERKEHRENDEN Posten und die abgerechneten Perioden dagegen für
// den gesamten Betrieb geladen. Für die Auskunft über eine Person las das Portal
// also recurring_charges und den halben Rechnungsbestand komplett (Hundert 36).
//
// Der Test hält beide Hälften fest: die gescopete Antwort muss für ihre Person
// weiterhin STIMMEN (die wiederkehrende Abgrenzung darf nicht verlorengehen), und
// sie darf FREMDE Personen nicht mehr mitführen.
func TestOutstandingScopedDoesNotLoadOtherPeople(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	mkPerson := func(first string) int64 {
		t.Helper()
		var id int64
		if err := h.Pool.QueryRow(ctx,
			`INSERT INTO persons (first_name, last_name) VALUES ($1,'Integration') RETURNING id`, first).Scan(&id); err != nil {
			t.Fatalf("insert person %s: %v", first, err)
		}
		return id
	}
	mkRecurring := func(pid int64, amount float64) {
		t.Helper()
		start := h.now().AddDate(0, -3, 0).Format("2006-01-02")
		if _, err := h.Pool.Exec(ctx,
			`INSERT INTO recurring_charges (person_id, description, amount, period, start_date)
			 VALUES ($1, 'Strom', $2, 'monthly', $3)`, pid, amount, start); err != nil {
			t.Fatalf("insert recurring for %d: %v", pid, err)
		}
	}

	mine := mkPerson("Scoped")
	other := mkPerson("Fremd")
	mkRecurring(mine, 10)
	mkRecurring(other, 999)

	req := httptest.NewRequest(http.MethodGet, "/api/persons/outstanding", nil)
	all, err := h.outstandingByPerson(req, 0)
	if err != nil {
		t.Fatalf("outstanding(all): %v", err)
	}
	scoped, err := h.outstandingByPerson(req, mine)
	if err != nil {
		t.Fatalf("outstanding(scoped): %v", err)
	}

	// (a) Die Zahl muss dieselbe bleiben — sonst hätte das Scoping die
	// wiederkehrende Abgrenzung stillschweigend unterschlagen.
	if math.Abs(scoped[mine]-all[mine]) > 0.05 {
		t.Errorf("gescopet %.2f weicht von der Gesamtsicht %.2f ab", scoped[mine], all[mine])
	}
	// Und sie darf nicht null sein, sonst prüfte (a) zwei leere Werte gegeneinander.
	if scoped[mine] <= 0 {
		t.Errorf("die wiederkehrende Abgrenzung fehlt: Saldo %.2f", scoped[mine])
	}

	// (b) Die fremde Person darf in der gescopeten Antwort gar nicht vorkommen.
	if _, present := scoped[other]; present {
		t.Errorf("die gescopete Antwort führt die fremde Person %d mit (%.2f)", other, scoped[other])
	}
	if _, present := all[other]; !present {
		t.Error("Vorannahme falsch: in der Gesamtsicht muss die fremde Person vorkommen")
	}
}

// Der Filter muss auch bei loadAllRecurringCharges direkt greifen, nicht erst in
// der Auswertung darüber — sonst bleibt die Datenbankabfrage die alte.
func TestLoadAllRecurringChargesRespectsThePersonFilter(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	var pid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Filter','Integration') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("insert person: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO recurring_charges (person_id, description, amount, period, start_date)
		 VALUES ($1,'Strom',10,'monthly',$2)`, pid, h.now().Format("2006-01-02")); err != nil {
		t.Fatalf("insert recurring: %v", err)
	}
	now := time.Now()
	scoped, err := h.loadAllRecurringCharges(ctx, now, pid)
	if err != nil {
		t.Fatalf("scoped: %v", err)
	}
	if len(scoped) != 1 {
		t.Errorf("die gescopete Abfrage lieferte %d Personen, erwartet genau 1", len(scoped))
	}
	if len(scoped[pid]) != 1 {
		t.Errorf("die eigene Person hat %d Posten, erwartet 1", len(scoped[pid]))
	}
}
