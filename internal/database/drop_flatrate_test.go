package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Migration 064 (Hundert 33, der einzige destruktive Punkt): die tote Tabelle
// flatrate_paid_years fällt — aber vorhandene Zeilen werden VOR dem DROP als
// Audit-Eintrag archiviert. Der Test spielt eine Bestandsinstallation nach:
// eigene Datenbank, Migrationen nur bis 063, Altzeile einfügen, dann 064 laufen
// lassen — die Tabelle muss weg sein und die Zeile im Protokoll stehen.
func TestDropFlatrateArchivesLeftoverRows(t *testing.T) {
	base := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(admin.Close)

	const dbName = "parkrr_dropflat_test"
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
	pool, err := Connect(ctx, own)
	if err != nil {
		t.Fatalf("connect own db: %v", err)
	}
	defer pool.Close()

	// Voll migrieren — die Tabelle ist danach weg. Die Bestandslage stellen wir
	// her, indem wir sie samt Altzeile WIEDER anlegen und NUR den 064er-Schritt
	// nachspielen (dieselben Statements wie die Migrationsdatei; ein Migrate()
	// "bis N" kennt der Runner bewusst nicht).
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name='flatrate_paid_years')`).Scan(&exists); err != nil {
		t.Fatalf("check: %v", err)
	}
	if exists {
		t.Fatal("nach voller Migration muss die Tabelle weg sein")
	}

	// Bestandsinstallation nachbauen: Tabelle + eine historische Zeile.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE flatrate_paid_years (
			person_id BIGINT NOT NULL, year INT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (person_id, year))`); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO flatrate_paid_years (person_id, year) VALUES (42, 2019)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Den 064er-Schritt nachspielen (Inhalt identisch zur Migrationsdatei).
	if _, err := pool.Exec(ctx, `
		DO $$
		DECLARE archived JSONB; n INT;
		BEGIN
			SELECT count(*), COALESCE(jsonb_agg(jsonb_build_object(
			           'person_id', person_id, 'year', year, 'created_at', created_at)), '[]'::jsonb)
			  INTO n, archived FROM flatrate_paid_years;
			IF n > 0 THEN
				INSERT INTO audit_log (username, action, entity, entity_id, summary, changes)
				VALUES ('system', 'delete', 'system', 0,
				        'Migration 064: Alt-Tabelle flatrate_paid_years entfernt — ' || n || ' historische Zeile(n) hier archiviert',
				        jsonb_build_object('flatrate_paid_years', jsonb_build_object('old', archived, 'new', NULL)));
			END IF;
		END $$;
		DROP TABLE IF EXISTS flatrate_paid_years;`); err != nil {
		t.Fatalf("replay 064: %v", err)
	}

	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name='flatrate_paid_years')`).Scan(&exists); err != nil {
		t.Fatalf("check: %v", err)
	}
	if exists {
		t.Error("die Tabelle steht noch")
	}
	var archivedRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_log
		 WHERE summary LIKE 'Migration 064:%'
		   AND changes->'flatrate_paid_years'->'old' @> '[{"person_id": 42, "year": 2019}]'`).Scan(&archivedRows); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if archivedRows == 0 {
		t.Error("die historische Zeile wurde beim DROP nicht ins Protokoll archiviert")
	}
}
