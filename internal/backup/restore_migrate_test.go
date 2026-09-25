package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/database"
)

// BAK-01: restoring an archive from an OLDER schema must leave a database that
// Migrate can bring forward. pg_restore --clean alone kept every object the archive
// did not know (here: 072's overview_revision_seq and trigger function) while
// schema_migrations came from the archive, so Migrate re-ran 072 and failed with
// "already exists" — on every later start, on every replica.
//
// Destructive: requires the dedicated disposable database parkrr_test_backup.
func TestRestoreOlderArchiveThenMigrate(t *testing.T) {
	dbURL := os.Getenv("PARKRR_BACKUP_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("PARKRR_BACKUP_TEST_DATABASE_URL not set")
	}
	for _, tool := range []string{"pg_dump", "pg_restore", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	isolateTempDir(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	pool, err := database.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var dbName string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	if dbName != "parkrr_test_backup" {
		t.Fatal("refusing restore test: dedicated database must be named parkrr_test_backup")
	}
	run := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	run(`DROP SCHEMA IF EXISTS parkrr_control CASCADE; DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public`)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	// Roll the live schema back to "071": undo 072/074 and forget them.
	run(`DO $$
		DECLARE t text;
		BEGIN
			FOREACH t IN ARRAY ARRAY['persons','categories','vehicles','flat_rate_periods',
				'flat_rate_period_vehicles','flat_rate_period_payments','charges',
				'recurring_charges','payments','invoices','invoice_source']
			LOOP
				EXECUTE format('DROP TRIGGER IF EXISTS overview_revision_bump ON %I', t);
			END LOOP;
		END $$;
		DROP FUNCTION parkrr_bump_overview_revision();
		DROP SEQUENCE overview_revision_seq;
		DELETE FROM schema_migrations
		 WHERE version IN ('072_overview_revision.sql', '074_overview_revision_fixes.sql')`)

	// The "pre-upgrade" backup.
	const key = "older-archive-restore-test-key"
	archive := filepath.Join(t.TempDir(), "old.dump.enc")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := DumpEncrypted(ctx, dbURL, key, f); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// The nightly verify streams the plaintext into pg_restore --list instead of
	// writing it to disk; it must still pass on a real archive ...
	if rep, err := VerifyArchiveFile(ctx, archive, key); err != nil || rep.Stage != "ok" {
		t.Fatalf("verify real archive: stage=%s err=%v", rep.Stage, err)
	}
	// ... and still authenticate every frame, although pg_restore stops reading
	// after the table of contents: a flipped byte near the END must fail.
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-40] ^= 0x01
	tampered := filepath.Join(t.TempDir(), "tampered.dump.enc")
	if err := os.WriteFile(tampered, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyArchiveFile(ctx, tampered, key); err == nil {
		t.Fatal("an archive with a corrupted tail passed verification")
	}
	if _, err := ValidateFile(ctx, tampered, key); err == nil {
		t.Fatal("an archive with a corrupted tail passed validation")
	}

	// Upgrade: the live database reaches HEAD again, then gains state the archive
	// does not have — including a restore job in parkrr_control that must survive.
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	run(`CREATE TABLE public.created_after_backup (id int)`)
	run(`INSERT INTO parkrr_control.restore_jobs
		(id, owner_instance, phase, source_name, source_path, checksum_sha256, requested_by)
		VALUES (repeat('a', 32), repeat('b', 32), 'restoring', 'old.dump.enc', '/x',
		        repeat('c', 64), 'test')`)

	if err := RestoreFile(ctx, dbURL, archive, key); err != nil {
		t.Fatalf("restore: %v", err)
	}
	pool.Reset()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate after restoring an older archive: %v", err)
	}

	var seq, stray, jobs int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM pg_class WHERE relname = 'overview_revision_seq'),
		(SELECT count(*) FROM pg_class WHERE relname = 'created_after_backup'),
		(SELECT count(*) FROM parkrr_control.restore_jobs)`).Scan(&seq, &stray, &jobs); err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Errorf("overview_revision_seq count = %d after migrate, want 1", seq)
	}
	if stray != 0 {
		t.Error("an object outside the archive survived the restore")
	}
	if jobs != 1 {
		t.Errorf("parkrr_control.restore_jobs rows = %d, want 1 (restore state must survive)", jobs)
	}
	run(`DELETE FROM parkrr_control.restore_jobs`)
}
