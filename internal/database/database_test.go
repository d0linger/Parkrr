package database

import (
	"context"
	"os"
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
	_, err := pool.Exec(ctx,
		`INSERT INTO audit_log (username, action, entity, summary, created_at)
		 VALUES ('tester','create','test','append-only probe', now() - interval '30 days')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// UPDATE and unguarded DELETE must be rejected by the trigger.
	if _, err := pool.Exec(ctx, `UPDATE audit_log SET summary='tampered' WHERE username='tester'`); err == nil {
		t.Fatal("UPDATE on audit_log should be blocked")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_log WHERE username='tester'`); err == nil {
		t.Fatal("ad-hoc DELETE on audit_log should be blocked")
	}

	// The sanctioned retention path (opts into the guard exception) may prune.
	if _, err := PruneAuditLog(ctx, pool, 24*time.Hour, 0); err != nil {
		t.Fatalf("PruneAuditLog: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE username='tester'`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("retention prune should have removed the probe row, %d remain", remaining)
	}
}
