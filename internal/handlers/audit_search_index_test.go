package handlers

import (
	"context"
	"strings"
	"testing"
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
	ctx := context.Background()

	var hasTrgm bool
	if err := h.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname='pg_trgm')`).Scan(&hasTrgm); err != nil {
		t.Fatalf("check pg_trgm: %v", err)
	}
	if !hasTrgm {
		t.Skip("pg_trgm nicht installiert — Migration 055 legt dann bewusst keinen Index an")
	}

	// Der Planer wählt bei einer FAST LEEREN Tabelle zu Recht den Seq Scan: ihn zu
	// lesen ist dann billiger als den Index zu befragen. Damit die Aussage etwas
	// wert ist, braucht es genug Zeilen — sonst prüfte der Test die Statistik, nicht
	// den Index.
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n < 500 {
		t.Skipf("nur %d Audit-Zeilen — zu wenig, als dass ein Index-Plan aussagekräftig wäre", n)
	}

	rows, err := h.Pool.Query(ctx,
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
	for _, want := range []string{"idx_audit_username_trgm", "idx_audit_summary_trgm"} {
		if !strings.Contains(got, want) {
			t.Errorf("die Audit-Suche benutzt %s nicht:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Seq Scan on audit_log") {
		t.Errorf("die Audit-Suche liest weiterhin die ganze Tabelle:\n%s", got)
	}
}
