package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Die Rechnung läuft jetzt auf einer eingebetteten Unicode-Schrift (Hundert 14).
// Vorher übersetzte ein cp1252-Umweg den Text: Deutsch ging, aber ein Kunde
// namens "Łukasz Nováković" stand als Ersatzzeichen auf seiner eigenen Rechnung —
// einem Dokument mit Aufbewahrungspflicht. Der Test rendert eine echte Rechnung
// für genau so einen Namen und prüft, dass ein gültiges, nicht-triviales PDF
// entsteht und die eingebettete Schrift (nicht die cp1252-Kernschrift) darin
// referenziert wird.
func TestInvoicePDFHandlesNonLatin1Names(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	var pid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name, address) VALUES ('Łukasz','Integration','Šafaříkova 12, Praha') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("person: %v", err)
	}
	var invID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO invoices (number, person_id, subtotal, total, note, buyer_snapshot)
		 VALUES ('PDF-UTF8-0001', $1, 50, 50, 'Stellplatz für Łukasz — Šarže №7',
		         '{"name":"Łukasz Nováković","address":"Šafaříkova 12, Praha"}'::jsonb)
		 RETURNING id`, pid).Scan(&invID); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_ = purgeExec(c, h.Pool, `DELETE FROM invoices WHERE id=$1`, invID)
		_ = purgeExec(c, h.Pool, `DELETE FROM persons WHERE id=$1`, pid)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/invoices/"+strconv.FormatInt(invID, 10)+"/pdf", nil)
	req.SetPathValue("id", strconv.FormatInt(invID, 10))
	rec := httptest.NewRecorder()
	h.InvoicePDF(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pdf: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatal("keine PDF-Datei")
	}
	// Die eingebettete Schrift muss im Dokument stehen; ein Rückfall auf die
	// cp1252-Kernschrift würde hier auffliegen. fpdf schreibt den Familiennamen
	// kleingeschrieben mit utf8-Präfix als BaseFont ("utf8dejavu") und die
	// TrueType-Daten als /FontFile2 — geprüft am erzeugten Dokument.
	if !bytes.Contains(body, []byte("utf8dejavu")) || !bytes.Contains(body, []byte("/FontFile2")) {
		t.Error("die eingebettete Unicode-Schrift fehlt im PDF — läuft die Rechnung wieder über cp1252?")
	}
	if bytes.Contains(body, []byte("Helvetica")) {
		t.Error("die cp1252-Kernschrift steht noch im Dokument")
	}
	// Subsetting-Sanity: das PDF muss deutlich KLEINER sein als die eingebettete
	// Schriftdatei (757 KB) — sonst wanderte die ganze Schrift in jedes Dokument.
	if len(body) > 400_000 {
		t.Errorf("PDF ist %d Bytes groß — wird die Schrift nicht subsettet?", len(body))
	}
}
