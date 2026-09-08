package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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
// ownInvoiceDB legt eine EIGENE, frisch migrierte Datenbank an und raeumt sie
// wieder ab. Der Rechnungslauf fegt ueber ALLE Personen — auf der geteilten
// Test-DB wuerde er die Personen parallel laufender Tests mit-fakturieren.
func ownInvoiceDB(t *testing.T, dbName string) (context.Context, *pgxpool.Pool, *handlers.Handler) {
	t.Helper()
	base := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	admin, err := database.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(admin.Close)

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
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, pool, handlers.New(pool)
}

func TestAutoInvoiceRunIsIdempotent(t *testing.T) {
	ctx, pool, h := ownInvoiceDB(t, "parkrr_autoinvoice_test")

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

// Ein einzelner unvollstaendiger Datensatz darf den Lauf NICHT anhalten.
//
// Der 422-Zweig kannte frueher nur "Pflichtangaben fehlen — abbrechen". Dieselbe
// 422 feuert aber auch fuer einen PERSONENBEZOGENEN Mangel: ab 400 EUR brutto
// verlangt § 11 UStG die Anschrift des Empfaengers. Eine Person ohne Anschrift
// stoppte damit die Fakturierung ALLER nachfolgenden Personen — jede Nacht, und
// die Zusammenfassung erwaehnte es nicht einmal.
//
// Der Test setzt die lueckenhafte Person BEWUSST auf die kleinere id, damit sie
// zuerst drankommt (der Lauf geht ORDER BY id).
func TestAutoInvoiceSkipsIncompletePersonAndKeepsGoing(t *testing.T) {
	ctx, pool, h := ownInvoiceDB(t, "parkrr_autoinvoice_skip_test")

	if _, err := pool.Exec(ctx, `
		UPDATE billing_settings SET seller_name='Auto GmbH', seller_address='Weg 1, 1010 Wien',
		       kleinunternehmer=true WHERE id=1`); err != nil {
		t.Fatalf("seller: %v", err)
	}
	// Zuerst die lueckenhafte Person: keine Anschrift, Betrag ueber 400 EUR.
	var bad int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name, address) VALUES ('Ohne','Anschrift','') RETURNING id`).
		Scan(&bad); err != nil {
		t.Fatalf("person ohne Anschrift: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO charges (person_id, description, amount) VALUES ($1, 'Grosser Posten', 500.00)`, bad); err != nil {
		t.Fatalf("charge: %v", err)
	}
	// Danach eine vollstaendige Person, die fakturiert werden MUSS.
	var good int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name, address) VALUES ('Mit','Anschrift','Testweg 2, Graz') RETURNING id`).
		Scan(&good); err != nil {
		t.Fatalf("person mit Anschrift: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO charges (person_id, description, amount) VALUES ($1, 'Kleiner Posten', 25.00)`, good); err != nil {
		t.Fatalf("charge: %v", err)
	}

	runAutoInvoice(pool, h)

	count := func(pid int64) int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE person_id=$1`, pid).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	if got := count(bad); got != 0 {
		t.Errorf("die Person ohne Anschrift haette keine Rechnung bekommen duerfen, hat %d", got)
	}
	if got := count(good); got != 1 {
		t.Fatalf("die vollstaendige Person wurde NICHT fakturiert (%d Rechnungen) — "+
			"ein einzelner lueckenhafter Datensatz hat den Lauf angehalten", got)
	}
	// Und der Lauf muss den Uebersprung im Protokoll benennen, statt "alles gut" zu melden.
	var summary string
	if err := pool.QueryRow(ctx,
		`SELECT summary FROM audit_log WHERE summary LIKE 'Automatischer Rechnungslauf%' ORDER BY id DESC LIMIT 1`).
		Scan(&summary); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(summary, "Empfaengerdaten") && !strings.Contains(summary, "Empfängerdaten") {
		t.Errorf("die Zusammenfassung verschweigt die uebersprungene Person: %q", summary)
	}
}
