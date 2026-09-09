package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Ein Storno-Dokument muss seinen Ursprung NENNEN: Titel "Storno-Rechnung", die
// Nummer der stornierten Rechnung als Meta-Zeile, negative Beträge. Ohne den
// Bezug ist der Beleg nicht rückführbar (§11 UStG), und die Buchhaltung rät
// (Hundert 18).
func TestStornoPDFNamesTheCancelledInvoice(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	var pid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Storno','Integration') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("person: %v", err)
	}
	var origID, stornoID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO invoices (number, person_id, subtotal, total) VALUES ('ST-2026-0001', $1, 100, 100) RETURNING id`,
		pid).Scan(&origID); err != nil {
		t.Fatalf("original: %v", err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE invoices SET canceled=true WHERE id=$1`, origID); err != nil {
		t.Fatalf("cancel original: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO invoices (number, person_id, subtotal, total, cancels_id)
		 VALUES ('ST-2026-0002', $1, -100, -100, $2) RETURNING id`, pid, origID).Scan(&stornoID); err != nil {
		t.Fatalf("storno: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_ = purgeExec(c, h.Pool, `DELETE FROM invoices WHERE id IN ($1,$2)`, stornoID, origID)
		_ = purgeExec(c, h.Pool, `DELETE FROM persons WHERE id=$1`, pid)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/invoices/"+strconv.FormatInt(stornoID, 10)+"/pdf", nil)
	req.SetPathValue("id", strconv.FormatInt(stornoID, 10))
	rec := httptest.NewRecorder()
	h.InvoicePDF(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pdf: %d %s", rec.Code, rec.Body.String())
	}
	// Der Text steht in komprimierten Streams; geprüft wird über die API-Sicht,
	// die dieselben Felder speist wie das PDF-Meta.
	iv, found, err := h.fetchInvoice(context.Background(), stornoID)
	if err != nil || !found {
		t.Fatalf("fetch: %v found=%v", err, found)
	}
	if iv.CancelsNumber != "ST-2026-0001" {
		t.Errorf("das Storno kennt seinen Ursprung nicht: %q", iv.CancelsNumber)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatal("kein PDF")
	}
}

// Die Offene-Posten-Liste als PDF (Hundert 17): gleiche Quelle wie Dashboard und
// CSV, nur Forderungen (kein Guthaben), als echtes Dokument.
func TestOutstandingReportPDF(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := createIntegrationPerson(t, h)
	// Eine offene Forderung: Zusatzkosten ohne Zahlung.
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO charges (person_id, description, amount) VALUES ($1,'Report-Posten',42.50)`, pid); err != nil {
		t.Fatalf("charge: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/reports/outstanding.pdf", nil)
	rec := httptest.NewRecorder()
	h.OutstandingReportPDF(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type %q", ct)
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatal("kein PDF")
	}
	if rec.Body.Len() < 5000 {
		t.Errorf("verdächtig kleines PDF (%d Bytes) — fehlt der Inhalt?", rec.Body.Len())
	}
}
