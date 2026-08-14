package handlers

import (
	"bytes"
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

	for _, ent := range []string{"outstanding", "payments", "persons", "vehicles"} {
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
