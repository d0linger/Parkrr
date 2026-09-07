package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to the database named by PARKRR_TEST_DATABASE_URL, skipping
// the test when it is not set (so the suite still runs without a database).
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set; skipping database integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestMigrateIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Running again must be a no-op (all versions already recorded).
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if n == 0 {
		t.Fatal("expected schema_migrations to be populated")
	}
}

func TestAuditLogAppendOnly(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Backdated so a POSITIVE retention window can actually reach it. The row used to
	// be inserted at now() and pruned with keep=-1h, which only worked because a
	// negative keep put the cutoff in the FUTURE. PruneAuditLog now treats keep <= 0
	// as "keep forever" and skips the long pass entirely, so that trick silently
	// stopped pruning anything — the test has to age the row instead.
	// Entity 'test' is outside auditKeepForeverEntities and action 'create' is
	// outside auditShortLivedActions, so the LONG pass is what deletes it.
	// Keep the row's id: the assertions below target THIS row rather than counting
	// every 'tester' row in the table. audit_log is append-only and the test DB is
	// reused, so a count could be perturbed by anything else that ever wrote under
	// that username, and would then fail for a reason unrelated to retention.
	var probeID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO audit_log (username, action, entity, summary, created_at)
		 VALUES ('tester','create','test','append-only probe', now() - interval '30 days')
		 RETURNING id`).Scan(&probeID); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// UPDATE and unguarded DELETE must be rejected by the trigger.
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET summary='tampered' WHERE id=$1`, probeID); err == nil {
		t.Fatal("UPDATE on audit_log should be blocked")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE id=$1`, probeID); err == nil {
		t.Fatal("ad-hoc DELETE on audit_log should be blocked")
	}

	// The sanctioned retention path (opts into the guard exception) may prune.
	if _, err := PruneAuditLog(ctx, pool, 24*time.Hour, 0); err != nil {
		t.Fatalf("PruneAuditLog: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE id=$1`, probeID).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("retention prune should have removed probe row %d", probeID)
	}
}

// TestMigrateDetectsEditedMigration: Versionen waren nur nach Dateinamen verbucht,
// eine nachträglich editierte Migration lief also nie erneut und zwei Installationen
// konnten unbemerkt divergieren (Hundert API-34). Migrate vergleicht jetzt eine
// SHA-256 und bricht laut ab.
//
// Der Test legt eine EIGENE Datenbank an: er verstellt eine Zeile in
// schema_migrations, und die Testpakete teilen sich sonst eine Instanz — auf der
// gemeinsamen DB würde er parallel laufende Migrate()-Aufrufe anderer Pakete
// sabotieren.
func TestMigrateDetectsEditedMigration(t *testing.T) {
	base := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set")
	}
	ctx := t.Context()
	admin, err := Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()

	const dbName = "parkrr_checksum_test"
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
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("baseline migrate: %v", err)
	}

	// Eine angewandte Migration "verändern", indem die gespeicherte Summe verstellt
	// wird. Der kurze Wert prüft nebenbei, dass die Fehlermeldung nicht über das Ende
	// hinaus schneidet.
	const victim = "001_init.sql"
	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET checksum='short' WHERE version=$1`, victim); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	err = Migrate(ctx, pool)
	if err == nil {
		t.Fatal("Migrate muss eine nachträglich veränderte Migration ablehnen")
	}
	if !strings.Contains(err.Error(), "modified after it was applied") {
		t.Errorf("unerwarteter Fehler: %v", err)
	}

	// Und der Altbestand-Pfad: NULL wird adoptiert, nicht als Manipulation gewertet.
	if _, err := pool.Exec(ctx, `UPDATE schema_migrations SET checksum=NULL`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Errorf("Migrationen ohne gespeicherte Summe müssen adoptiert werden, nicht scheitern: %v", err)
	}
}
