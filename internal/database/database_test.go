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
	// Als Cleanup statt defer: defers laufen VOR den t.Cleanup-Callbacks, ein
	// defer admin.Close() würde also das unten registrierte DROP DATABASE auf
	// einem geschlossenen Pool laufen lassen (Fehler verworfen, DB bleibt liegen).
	// Cleanups laufen LIFO — erst der Drop, dann dieses Close.
	t.Cleanup(admin.Close)

	const dbName = "parkrr_checksum_test"
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

// Die Prüfsumme läuft ZEILENENDEN-UNABHÄNGIG. Der Test hält beide Richtungen fest,
// denn nur eine davon war abgedeckt: eine Installation, die ihre Summe über CRLF-Bytes
// aufgezeichnet hat und jetzt LF eingebettet bekommt (genau das, was `*.sql -text` in
// .gitattributes auslöst), lief in den Startabbruch, dessen eigener Rat („add a NEW
// migration instead") nicht hilft — der aufgezeichnete Wert ließe sich nur noch von
// Hand in der Datenbank korrigieren.
func TestMigrationChecksumIgnoriertZeilenenden(t *testing.T) {
	lf := []byte("CREATE TABLE x (id int);\nSELECT 1;\n")
	crlf := []byte("CREATE TABLE x (id int);\r\nSELECT 1;\r\n")
	if migrationChecksum(lf) != migrationChecksum(crlf) {
		t.Fatal("dieselbe Migration mit anderen Zeilenenden muss dieselbe Summe ergeben")
	}
	if migrationChecksum(lf) == migrationChecksum([]byte("SELECT 2;\n")) {
		t.Fatal("eine inhaltliche Änderung muss weiterhin auffallen")
	}
	// Beide Altbestand-Richtungen werden als „dieselbe Datei, nur anders gezählt"
	// erkannt: roh wie eingebettet, und roh als CRLF ausgecheckt.
	for name, stored := range map[string]string{
		"roh wie eingebettet": sha256Hex(lf),
		"roh als CRLF":        sha256Hex(crlf),
	} {
		t.Run(name, func(t *testing.T) {
			if !legacyChecksumMatch(lf, stored) {
				t.Error("alte Summe muss als dieselbe Datei erkannt werden")
			}
		})
	}
	if legacyChecksumMatch(lf, sha256Hex([]byte("SELECT 2;\n"))) {
		t.Error("eine fremde Summe darf nicht als Altbestand durchgehen")
	}
}

// Ein zweiter Start darf die aufgezeichneten Summen NICHT neu schreiben. Der
// Aufhebe-Zweig für Altbestand stand ohne den Normalfall davor: für jede Datei ohne CR
// sind rohe und normalisierte Summe identisch, er traf also bei JEDEM Start auf ALLE
// diese Migrationen zu und schrieb den Wert zurück, der schon dort stand — Dutzende
// überflüssige UPDATE je Prozessstart, unter der Migrations-Sperre.
func TestMigrateSchreibtPruefsummenNichtBeiJedemStartNeu(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// xmin ist die schreibende Transaktion je Zeile: ändert sie sich, wurde die Zeile
	// angefasst. Ein reiner Verifikationslauf darf keine einzige anfassen.
	before := map[string]int64{}
	rows, err := pool.Query(ctx, `SELECT version, xmin::text::bigint FROM schema_migrations`)
	if err != nil {
		t.Fatalf("read xmin: %v", err)
	}
	for rows.Next() {
		var v string
		var x int64
		if err := rows.Scan(&v, &x); err != nil {
			t.Fatalf("scan: %v", err)
		}
		before[v] = x
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatalf("read xmin: %v", rows.Err())
	}
	if len(before) == 0 {
		t.Fatal("keine Migrationen aufgezeichnet — der Test prüft nichts")
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("zweiter Migrate: %v", err)
	}
	rows, err = pool.Query(ctx, `SELECT version, xmin::text::bigint FROM schema_migrations`)
	if err != nil {
		t.Fatalf("read xmin again: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var x int64
		if err := rows.Scan(&v, &x); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if old, ok := before[v]; ok && old != x {
			t.Errorf("%s wurde beim zweiten Start ohne Not neu geschrieben", v)
		}
	}
}
