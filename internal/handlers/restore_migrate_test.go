package handlers

import (
	"context"
	"testing"

	"github.com/preining/parkrr/internal/database"
)

// TestReconcileSchemaAfterRestoreReappliesMigrations: restoring a backup that
// predates a migration leaves the schema a version behind the running binary
// (missing columns → "query failed"). The post-restore reconcile must bring it
// forward in one shot, so no second container restart is needed.
func TestReconcileSchemaAfterRestoreReappliesMigrations(t *testing.T) {
	h := testHandler(t)
	ctx := t.Context()
	// The test DB is shared; guarantee it is left fully migrated even if an
	// assertion below fails after we deliberately drop part of the schema.
	t.Cleanup(func() { _ = database.Migrate(context.Background(), h.Pool) })

	// Simulate restoring a pre-036 backup: its schema_migrations and the columns
	// migration 036 added are the older ones (the unique index drops with them).
	if _, err := h.Pool.Exec(ctx,
		`ALTER TABLE payments DROP COLUMN IF EXISTS settles_kind,
		 DROP COLUMN IF EXISTS settles_ref, DROP COLUMN IF EXISTS settles_period`); err != nil {
		t.Fatalf("simulate old schema (drop columns): %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`DELETE FROM schema_migrations WHERE version='036_period_payment_link.sql'`); err != nil {
		t.Fatalf("simulate old schema (migrations row): %v", err)
	}
	// Precondition: a query using the new column now fails — the "query failed" state.
	if _, err := h.Pool.Exec(ctx, `SELECT settles_kind FROM payments LIMIT 1`); err == nil {
		t.Fatalf("precondition: settles_kind should be gone after the simulated old restore")
	}

	// The reconcile that runs at the end of a restore must fix it without a restart.
	if err := h.reconcileSchemaAfterRestore(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// 036 is recorded again, the columns are back, and the previously-failing query runs.
	var has bool
	if err := h.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version='036_period_payment_link.sql')`).Scan(&has); err != nil || !has {
		t.Errorf("migration 036 must be re-applied after restore (has=%v err=%v)", has, err)
	}
	if _, err := h.Pool.Exec(ctx, `SELECT count(*) FROM payments WHERE settles_kind IS NOT NULL`); err != nil {
		t.Errorf("settles_* columns must exist after reconcile: %v", err)
	}
}
