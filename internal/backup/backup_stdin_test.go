package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	for _, tool := range []string{"pg_dump", "pg_restore", "psql"} {
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
			`DROP SCHEMA IF EXISTS backup_stdin_dep CASCADE; DROP SCHEMA backup_stdin_test CASCADE`); err != nil {
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
	// The dependent view lives in its own schema: the restore replaces public as a
	// whole, so a view there would simply be dropped with it.
	if _, err := conn.Exec(ctx, `UPDATE backup_stdin_test.payload SET data='preserved' WHERE id=1;
		CREATE SCHEMA backup_stdin_dep;
		CREATE VIEW backup_stdin_dep.dependency AS SELECT id FROM backup_stdin_test.payload`); err != nil {
		t.Fatal(err)
	}
	// A dependent object outside the archive forces a real SQL error. The target
	// must remain unchanged because the restore runs in a single transaction.
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
	if _, err := conn.Exec(ctx, `DROP SCHEMA backup_stdin_dep CASCADE`); err != nil {
		t.Fatal(err)
	}

	// A view OUTSIDE public that reads a public table would be dropped silently by
	// DROP SCHEMA public CASCADE. The restore must refuse instead and keep it.
	if _, err := conn.Exec(ctx, `CREATE TABLE public.backup_stdin_pub(id integer PRIMARY KEY);
		CREATE SCHEMA backup_stdin_ext;
		CREATE VIEW backup_stdin_ext.reads_public AS SELECT id FROM public.backup_stdin_pub`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(),
			`DROP SCHEMA IF EXISTS backup_stdin_ext CASCADE; DROP TABLE IF EXISTS public.backup_stdin_pub`); err != nil {
			t.Error(err)
		}
	}()
	err = Restore(ctx, dbURL, enc, key)
	if err == nil || !strings.Contains(err.Error(), "backup_stdin_ext.reads_public") {
		t.Fatalf("restore over an external dependency on public: err = %v, want a refusal naming the view", err)
	}
	var viewExists bool
	if err := conn.QueryRow(ctx,
		`SELECT to_regclass('backup_stdin_ext.reads_public') IS NOT NULL`).Scan(&viewExists); err != nil {
		t.Fatal(err)
	}
	if !viewExists {
		t.Fatal("the refused restore dropped the external view")
	}
	if _, err := conn.Exec(ctx, `DROP SCHEMA backup_stdin_ext CASCADE`); err != nil {
		t.Fatal(err)
	}

	// A column OUTSIDE public whose type is declared in public would be dropped,
	// data and all, by the CASCADE. The restore must refuse and keep it.
	if _, err := conn.Exec(ctx, `CREATE TYPE public.backup_stdin_mood AS ENUM ('ok', 'bad');
		CREATE SCHEMA backup_stdin_typed;
		CREATE TABLE backup_stdin_typed.rows(id integer PRIMARY KEY, mood public.backup_stdin_mood);
		INSERT INTO backup_stdin_typed.rows VALUES (1, 'bad')`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(),
			`DROP SCHEMA IF EXISTS backup_stdin_typed CASCADE; DROP TYPE IF EXISTS public.backup_stdin_mood`); err != nil {
			t.Error(err)
		}
	}()
	err = Restore(ctx, dbURL, enc, key)
	if err == nil || !strings.Contains(err.Error(), "backup_stdin_typed.rows.mood") {
		t.Fatalf("restore over a column typed from public: err = %v, want a refusal naming the column", err)
	}
	var mood string
	if err := conn.QueryRow(ctx, `SELECT mood::text FROM backup_stdin_typed.rows WHERE id = 1`).Scan(&mood); err != nil {
		t.Fatalf("the refused restore lost the typed column: %v", err)
	}
	if mood != "bad" {
		t.Fatalf("typed column data changed: %q", mood)
	}
	if _, err := conn.Exec(ctx, `DROP SCHEMA backup_stdin_typed CASCADE`); err != nil {
		t.Fatal(err)
	}

	// Transitively: a domain OUTSIDE public over a public type, used by an external
	// column. The column's own type is not in public, but the CASCADE would drop
	// the domain and, with it, the column.
	if _, err := conn.Exec(ctx, `CREATE SCHEMA backup_stdin_domain;
		CREATE DOMAIN backup_stdin_domain.mood_d AS public.backup_stdin_mood;
		CREATE TABLE backup_stdin_domain.rows(id integer PRIMARY KEY, mood backup_stdin_domain.mood_d);
		INSERT INTO backup_stdin_domain.rows VALUES (1, 'ok')`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `DROP SCHEMA IF EXISTS backup_stdin_domain CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	err = Restore(ctx, dbURL, enc, key)
	if err == nil || !strings.Contains(err.Error(), "backup_stdin_domain.mood_d") {
		t.Fatalf("restore over an external domain on a public type: err = %v, want a refusal naming the domain", err)
	}
	if err := conn.QueryRow(ctx, `SELECT mood::text FROM backup_stdin_domain.rows WHERE id = 1`).Scan(&mood); err != nil {
		t.Fatalf("the refused restore lost the domain-typed column: %v", err)
	}
	if mood != "ok" {
		t.Fatalf("domain-typed column data changed: %q", mood)
	}
}
