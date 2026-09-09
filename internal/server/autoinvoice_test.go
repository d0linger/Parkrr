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

	// Anlegen mit Wiederholung statt sofortigem Skip: go test ./... laesst Pakete
	// PARALLEL laufen, und zwei gleichzeitige CREATE DATABASE aus derselben Vorlage
	// weist Postgres ab ("source database template1 is being accessed by other
	// users"). Unter -race (CI) ist alles um ein Vielfaches langsamer und das
	// Fenster entsprechend weit offen — der sofortige Skip machte daraus einen
	// STILLEN Ausfall des ganzen Pakets in der Abdeckung. Erst ein DAUERHAFTER
	// Fehler (etwa fehlendes CREATEDB-Recht einer lokalen Umgebung) bleibt ein Skip.
	var derr error
	for i := 0; i < 20; i++ {
		if _, derr = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+dbName+` WITH (FORCE)`); derr == nil {
			if _, derr = admin.Exec(ctx, `CREATE DATABASE `+dbName); derr == nil {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if derr != nil {
		t.Skipf("kann keine eigene Test-DB anlegen: %v", derr)
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

// TestMerkerUnterscheidetNieGelaufenVonNichtLesbar ist der erste Test des
// Merker-Pfades ueberhaupt — bis hierher deckte keiner der beiden Tests dieser Datei
// loadAutoInvoiceLast, saveAutoInvoiceLast oder auto_invoice_status ab.
//
// Er haelt die Unterscheidung fest, an der vorher alles haengenblieb: "noch nie
// gelaufen" und "gerade nicht lesbar" sahen gleich aus, weil beide "jetzt" ergaben.
// Auf dem Stand VORHER war die Signatur time.Time ohne Fehler, dieser Test also gar
// nicht schreibbar; er faellt auf jeder Rueckkehr zu jener Form sofort um.
func TestMerkerUnterscheidetNieGelaufenVonNichtLesbar(t *testing.T) {
	ctx, pool, _ := ownInvoiceDB(t, "parkrr_marker_test")

	// 1. Frisch migriert: die Zeile steht, last_run_at ist NULL — noch nie gelaufen.
	got, err := loadAutoInvoiceLast(pool)
	if err != nil {
		t.Fatalf("frische Migration darf keinen Fehler liefern: %v", err)
	}
	if got != nil {
		t.Fatalf("noch nie gelaufen muss nil sein, war %v", got)
	}

	// 2. Nach einem Lauf: der Zeitpunkt kommt zurueck.
	want := time.Now().Add(-90 * time.Minute).Truncate(time.Second)
	saveAutoInvoiceLast(pool, want)
	got, err = loadAutoInvoiceLast(pool)
	if err != nil {
		t.Fatalf("nach dem Schreiben: %v", err)
	}
	if got == nil || !got.Truncate(time.Second).Equal(want) {
		t.Fatalf("Merker %v, erwartet %v", got, want)
	}

	// 3. Zeile von Hand entfernt: das ist KEIN Fehler, sondern wieder "nie gelaufen" —
	//    und saveAutoInvoiceLast legt sie danach wieder an (ON CONFLICT, kein blosses
	//    UPDATE, das lautlos null Zeilen traefe).
	if _, err := pool.Exec(ctx, `DELETE FROM auto_invoice_status`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err = loadAutoInvoiceLast(pool)
	if err != nil {
		t.Fatalf("fehlende Zeile darf kein Fehler sein: %v", err)
	}
	if got != nil {
		t.Fatalf("fehlende Zeile muss nil sein, war %v", got)
	}
	saveAutoInvoiceLast(pool, want)
	if got, err = loadAutoInvoiceLast(pool); err != nil || got == nil {
		t.Fatalf("saveAutoInvoiceLast muss die Zeile neu anlegen (got=%v err=%v)", got, err)
	}

	// 4. Tabelle weg = NICHT LESBAR. Der Punkt des ganzen Tests: hier muss ein FEHLER
	//    herauskommen und KEIN Zeitpunkt. Kaeme wie frueher "jetzt" zurueck, verschoebe
	//    eine voruebergehende Stoerung den naechsten Termin um eine volle Periode.
	if _, err := pool.Exec(ctx, `DROP TABLE auto_invoice_status`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	got, err = loadAutoInvoiceLast(pool)
	if err == nil {
		t.Fatal("unlesbarer Merker muss einen Fehler liefern, nicht schweigen")
	}
	if got != nil {
		t.Fatalf("bei einem Lesefehler darf KEIN Zeitpunkt behauptet werden, war %v", got)
	}
}

// Der Test zu backup.EffectiveLast — warum der Lauf den SPAETEREN von Speicher und
// Datenbank nimmt (Ruecksicherung unter laufendem Prozess) — liegt beim Besitzer
// der Funktion: internal/backup/scheduler_test.go,
// TestEffectiveLastIgnoriertEinenAelterenFremdenMerker.
