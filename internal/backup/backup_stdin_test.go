package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// This destructive restore test requires its own explicitly supplied disposable
// database, separate from both production and the ordinary handler test database.
func TestRestoreLargeArchiveWithoutTemporaryStorage(t *testing.T) {
	dbURL := os.Getenv("PARKRR_BACKUP_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("PARKRR_BACKUP_TEST_DATABASE_URL not set")
	}
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var dbName string
	if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	if dbName != "parkrr_test_backup" {
		t.Fatal("refusing restore test: dedicated database must be named parkrr_test_backup")
	}
	if _, err := conn.Exec(ctx, `CREATE SCHEMA backup_stdin_test;
		CREATE TABLE backup_stdin_test.payload(id integer PRIMARY KEY, data text);
		INSERT INTO backup_stdin_test.payload SELECT i, repeat('x', 1048576) FROM generate_series(1, 34) i`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(),
			`DROP VIEW IF EXISTS public.backup_stdin_dependency; DROP SCHEMA backup_stdin_test CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	dsn, env := dbExecEnv(dbURL)
	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--compress=0",
		"--schema=backup_stdin_test", "--no-owner", "--no-privileges", "--dbname="+dsn)
	cmd.Env = env
	plain, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) <= 32<<20 {
		t.Fatalf("fixture too small: %d bytes", len(plain))
	}
	const key = "disposable-backup-test-key"
	enc, err := Encrypt(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	plain = nil
	// A missing temporary directory made the previous implementation fail before
	// invoking pg_restore. Both validation and restore must now use stdin.
	unavailable := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("TMPDIR", unavailable)
	t.Setenv("TMP", unavailable)
	t.Setenv("TEMP", unavailable)
	if _, err := Validate(ctx, enc, key); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE backup_stdin_test.payload SET data='changed' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := Restore(ctx, dbURL, enc, key); err != nil {
		t.Fatal(err)
	}
	var length int
	if err := conn.QueryRow(ctx, `SELECT length(data) FROM backup_stdin_test.payload WHERE id=1`).Scan(&length); err != nil {
		t.Fatal(err)
	}
	if length != 1048576 {
		t.Fatalf("restored payload length = %d", length)
	}
	if err := Restore(ctx, dbURL, enc, "wrong-key"); err == nil {
		t.Fatal("wrong key restored")
	}
	if _, err := conn.Exec(ctx, `UPDATE backup_stdin_test.payload SET data='preserved' WHERE id=1;
		CREATE VIEW public.backup_stdin_dependency AS SELECT id FROM backup_stdin_test.payload`); err != nil {
		t.Fatal(err)
	}
	// A dependent object outside the archive forces a real SQL error. The target
	// must remain unchanged because pg_restore runs in a single transaction.
	if err := Restore(ctx, dbURL, enc, key); err == nil {
		t.Fatal("expected dependency failure")
	}
	var data string
	if err := conn.QueryRow(ctx, `SELECT data FROM backup_stdin_test.payload WHERE id=1`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if data != "preserved" {
		t.Fatal("failed restore changed existing data")
	}
}
