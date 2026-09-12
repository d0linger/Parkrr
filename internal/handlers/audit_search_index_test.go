package handlers

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Die Suche im Änderungsprotokoll lief als zwei ungeankerte ILIKE '%…%' und damit
// als Seq Scan über die gesamte Tabelle — über die, die wegen der 7-Jahres-Auf-
// bewahrung am schnellsten wächst (Hundert 35). Migration 055 legt dafür
// Trigramm-Indizes an.
//
// Der Test prüft den PLAN, nicht die Laufzeit: eine Zeitmessung wäre auf einer
// kleinen Testtabelle bedeutungslos und auf einer geteilten Datenbank zusätzlich
// launisch. "Benutzt Postgres den Index?" ist die Frage, die zählt.
func TestAuditSearchUsesTheTrigramIndex(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	var hasTrgm bool
	if err := h.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname='pg_trgm')`).Scan(&hasTrgm); err != nil {
		t.Fatalf("check pg_trgm: %v", err)
	}
	if !hasTrgm {
		t.Skip("pg_trgm nicht installiert — Migration 055 legt dann bewusst keinen Index an")
	}

	// Auch 500 bis 1500 zufällig vorhandene Testzeilen sind oft billiger sequenziell
	// zu lesen. Eigene, ausreichend große Historie statt eines Reihenfolge-abhängigen
	// Skips: seltene Treffer in BEIDEN Suchspalten, normale Einträge dazwischen.
	// Alles bleibt in dieser Transaktion und wird auch bei einem Fehler zurückgerollt.
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin audit fixture: %v", err)
	}
	var fixtureIDs []int64
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := tx.Rollback(cleanupCtx); err != nil {
			t.Errorf("rollback audit fixture: %v", err)
			return
		}
		if len(fixtureIDs) > 0 {
			var remaining int
			if err := h.Pool.QueryRow(cleanupCtx,
				`SELECT count(*) FROM audit_log WHERE id = ANY($1)`, fixtureIDs).Scan(&remaining); err != nil {
				t.Errorf("verify audit fixture rollback: %v", err)
			} else if remaining != 0 {
				t.Errorf("audit fixture left %d rows after rollback", remaining)
			} else {
				t.Logf("audit fixture rollback: %d inserted rows, %d remaining", len(fixtureIDs), remaining)
			}
		}
	}()
	if err := tx.QueryRow(ctx, `WITH inserted AS (
		INSERT INTO audit_log (username, action, entity, entity_id, summary, created_at)
		SELECT CASE WHEN i = 301 THEN 'zzqxmueller' ELSE 'operator-' || (i % 32) END,
		       'update', 'vehicle', i,
		       CASE WHEN i = 17003 THEN 'Gefährt für zzqxmueller umgestellt'
		            ELSE 'Gefährt ' || i || ': Stellplatz von Halle ' || (i % 31) ||
		                 ' nach Halle ' || ((i + 1) % 31) || ' geändert; Übergabe kontrolliert'
		       END,
		       now() - (i % 2555) * interval '1 day'
		FROM generate_series(1, 20000) AS fixture(i)
		RETURNING id
	) SELECT array_agg(id) FROM inserted`).Scan(&fixtureIDs); err != nil {
		t.Fatalf("seed audit fixture: %v", err)
	}
	// Der Bulk-Insert hinterlässt GIN-Pending-Listen. Diese Wartung übernimmt sonst
	// VACUUM; ohne sie würde der Test den vorübergehenden Ladezustand bewerten.
	for _, index := range []string{"idx_audit_username_trgm", "idx_audit_summary_trgm"} {
		var cleaned int64
		if err := tx.QueryRow(ctx, `SELECT gin_clean_pending_list($1::regclass)`, index).Scan(&cleaned); err != nil {
			t.Fatalf("clean pending entries for %s: %v", index, err)
		}
	}
	// Nicht auf Autovacuum warten: EXPLAIN soll die gerade angelegte Historie kennen.
	if _, err := tx.Exec(ctx, `ANALYZE audit_log`); err != nil {
		t.Fatalf("analyze audit fixture: %v", err)
	}

	rows, err := tx.Query(ctx,
		`EXPLAIN (COSTS OFF) SELECT id FROM audit_log
		  WHERE (username ILIKE $1 OR summary ILIKE $1)
		  ORDER BY created_at DESC, id DESC LIMIT 50`, "%zzqxmueller%")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read plan: %v", err)
	}
	got := plan.String()
	t.Logf("audit search plan:\n%s", got)
	for _, want := range []string{"idx_audit_username_trgm", "idx_audit_summary_trgm"} {
		if !strings.Contains(got, want) {
			t.Errorf("die Audit-Suche benutzt %s nicht:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Seq Scan on audit_log") {
		t.Errorf("die Audit-Suche liest weiterhin die ganze Tabelle:\n%s", got)
	}
}
