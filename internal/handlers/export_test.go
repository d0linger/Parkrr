package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercises the CSV export across all entities: correct status, UTF-8 BOM,
// semicolon header, .csv attachment, seeded data present, and 404 for an unknown
// entity. Runs only when PARKRR_TEST_DATABASE_URL is set (testHandler skips).
func TestExportCSV(t *testing.T) {
	h := testHandler(t)

	// Seed one person + one payment so persons/payments have real rows.
	pid := createIntegrationPerson(t, h)
	if rec := postPayment(t, h, pid, map[string]any{"amount": 12.5, "method": "bar"}); rec.Code != http.StatusCreated {
		t.Fatalf("seed payment: status %d body %s", rec.Code, rec.Body.String())
	}

	call := func(entity string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/export/"+entity, nil)
		req.SetPathValue("entity", entity)
		w := httptest.NewRecorder()
		h.ExportCSV(w, req)
		return w
	}

	for _, ent := range []string{"outstanding", "payments", "persons", "vehicles", "invoices", "charges"} {
		w := call(ent)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", ent, w.Code)
		}
		body := w.Body.Bytes()
		if !bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
			t.Errorf("%s: missing UTF-8 BOM", ent)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
			t.Errorf("%s: content-type %q", ent, ct)
		}
		if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, ".csv") || !strings.Contains(cd, "attachment") {
			t.Errorf("%s: content-disposition %q", ent, cd)
		}
		// Header line must be semicolon-separated (German Excel).
		firstLine := strings.SplitN(string(body[3:]), "\n", 2)[0]
		if !strings.Contains(firstLine, ";") {
			t.Errorf("%s: header not semicolon-separated: %q", ent, firstLine)
		}
	}

	// Seeded data shows up with the expected formatting.
	if got := call("persons").Body.String(); !strings.Contains(got, "Integration") {
		t.Error("persons export missing the seeded person")
	}
	pay := call("payments").Body.String()
	if !strings.Contains(pay, "Pay Integration") {
		t.Error("payments export missing the seeded person name")
	}
	if !strings.Contains(pay, "12,50") {
		t.Error("payments export should format the amount with a decimal comma (12,50)")
	}

	// Unknown entity -> 404.
	if w := call("bogus"); w.Code != http.StatusNotFound {
		t.Errorf("unknown export: want 404, got %d", w.Code)
	}
}

// Rechnungen und Zusatzkosten fehlten als einzige Geldarten im Export (Hundert 19):
// die Buchhaltung bekam Zahlungen und offene Posten, aber nicht die Belege, aus
// denen sie entstehen.
func TestExportInvoicesAndCharges(t *testing.T) {
	h := testHandler(t)
	ctx := t.Context()
	pid := createIntegrationPerson(t, h)

	// Eine Zusatzkostenzeile mit MENGE 3 — der Fall, an dem sich zeigt, ob der Export
	// den Einzelbetrag oder die Gesamtsumme meint.
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO charges (person_id, description, amount, quantity) VALUES ($1,'Stromanschluss',10.00,3)`,
		pid); err != nil {
		t.Fatalf("seed charge: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO invoices (number, person_id, subtotal, ust_rate, tax_amount, total, note)
		 VALUES ('EXP-2026-0001', $1, 100.00, 20.00, 20.00, 120.00, 'Testbeleg')`, pid); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_ = purgeExec(c, h.Pool, `DELETE FROM invoices WHERE number='EXP-2026-0001'`)
	})

	call := func(entity string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/export/"+entity, nil)
		req.SetPathValue("entity", entity)
		w := httptest.NewRecorder()
		h.ExportCSV(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d %s", entity, w.Code, w.Body.String())
		}
		return w.Body.String()
	}

	inv := call("invoices")
	for _, want := range []string{"EXP-2026-0001", "100,00", "20,00", "120,00", "Testbeleg"} {
		if !strings.Contains(inv, want) {
			t.Errorf("der Rechnungsexport enthält %q nicht", want)
		}
	}

	ch := call("charges")
	if !strings.Contains(ch, "Stromanschluss") {
		t.Error("der Zusatzkostenexport enthält die Position nicht")
	}
	// Einzelbetrag 10,00 UND Gesamtsumme 30,00 müssen beide dastehen — sonst addiert
	// die Buchhaltung die falsche Spalte.
	if !strings.Contains(ch, "10,00") {
		t.Error("der Einzelbetrag fehlt")
	}
	if !strings.Contains(ch, "30,00") {
		t.Error("die Gesamtsumme (Menge × Einzelbetrag) fehlt — genau die Spalte, die gebraucht wird")
	}
}
