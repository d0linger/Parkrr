package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/database"
	"github.com/preining/parkrr/internal/handlers"
)

// Der automatische Rechnungslauf (Hundert 16) nutzt DENSELBEN Handler wie der
// Knopf. Der Test belegt die Sicherheitseigenschaften: erstellt (offene
// abgeschlossene Posten) und IDEMPOTENT (der zweite Lauf direkt danach erstellt
// nichts — darauf beruht die Neustart-Sicherheit).
//
// In einer EIGENEN Datenbank, mit Bedacht: der Lauf fegt über ALLE Personen.
// Auf der geteilten Test-DB würde er die Personen parallel laufender
// Handler-Tests mit-fakturieren und deren Periodensperren scharf schalten —
// genau so ist TestAgreementSliderBooksPaymentAndSettlesExtras einmal
// umgefallen, während dieser Test hier noch auf der geteilten DB lief.
func TestAutoInvoiceRunIsIdempotent(t *testing.T) {
	base := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := database.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()

	const dbName = "parkrr_autoinvoice_test"
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`); err != nil {
		t.Skipf("kann keine eigene Test-DB anlegen: %v", err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
		t.Skipf("kann keine eigene Test-DB anlegen: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`)
	})
	own := base
	if i := strings.LastIndex(base, "/"); i >= 0 {
		rest := ""
		if j := strings.Index(base[i:], "?"); j >= 0 {
			rest = base[i:][j:]
		}
		own = base[:i+1] + dbName + rest
	}
	pool, err := database.Connect(ctx, own)
	if err != nil {
		t.Fatalf("connect own db: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := handlers.New(pool)

	// Verkäufer-Pflichtangaben (§11), sonst bricht der Lauf per 422 ab.
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
