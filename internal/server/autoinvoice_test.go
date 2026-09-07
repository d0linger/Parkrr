package server

import (
	"context"
	"testing"

	"github.com/preining/parkrr/internal/handlers"
)

// Der automatische Rechnungslauf (Hundert 16) nutzt DENSELBEN Handler wie der
// Knopf. Der Test belegt die drei Ausgänge, auf denen die Sicherheit beruht:
// erstellt (offene abgeschlossene Perioden), still übersprungen (nichts offen —
// und damit IDEMPOTENT: der zweite Lauf direkt danach erstellt nichts), und die
// Personen-Schleife erfasst alle.
func TestAutoInvoiceRunIsIdempotent(t *testing.T) {
	pool := testPool(t)
	h := handlers.New(pool)
	ctx := context.Background()

	// Verkäufer-Pflichtangaben (§11) setzen, sonst bricht der Lauf per 422 ab.
	if _, err := pool.Exec(ctx, `
		UPDATE billing_settings SET seller_name='Auto GmbH', seller_address='Weg 1, 1010 Wien',
		       kleinunternehmer=true WHERE id=1`); err != nil {
		t.Fatalf("seller: %v", err)
	}

	var pid int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name, address) VALUES ('Auto','RunSeed','Testweg 2, Graz') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("person: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO charges (person_id, description, amount) VALUES ($1, 'AutoRun-Posten', 25.00)`, pid); err != nil {
		t.Fatalf("charge: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		// Teardown über die purge-Ausnahme (Rechnungen sind unveränderlich).
		tx, err := pool.Begin(c)
		if err == nil {
			_, _ = tx.Exec(c, `SET LOCAL parkrr.purge = 'on'`)
			_, _ = tx.Exec(c, `DELETE FROM invoices WHERE person_id=$1`, pid)
			_, _ = tx.Exec(c, `DELETE FROM persons WHERE id=$1`, pid)
			_ = tx.Commit(c)
		}
	})

	countInvoices := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE person_id=$1`, pid).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	runAutoInvoice(pool, h)
	if got := countInvoices(); got != 1 {
		t.Fatalf("nach dem ersten Lauf: %d Rechnungen, erwartet 1", got)
	}
	// Der zweite Lauf DIREKT danach darf nichts erzeugen — die Periodensperre
	// macht ihn leer. Genau darauf beruht die Neustart-Sicherheit.
	runAutoInvoice(pool, h)
	if got := countInvoices(); got != 1 {
		t.Errorf("nach dem zweiten Lauf: %d Rechnungen — der Lauf ist nicht idempotent", got)
	}
}
