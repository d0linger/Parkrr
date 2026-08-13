package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func csvUpload(t *testing.T, content string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "personen.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/import/persons", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// A German-Excel CSV (';' + BOM) imports valid rows, skips duplicate e-mails
// (case-insensitive) and empty lines, and reports rows with no name as failed.
// Runs only when PARKRR_TEST_DATABASE_URL is set.
func TestImportPersonsSemicolonBOM(t *testing.T) {
	h := testHandler(t)
	csvData := "\ufeffvorname;nachname;email;telefon;adresse\n" +
		"Anna;Integration;anna.imp@example.com;+43 1;Wien\n" +
		"Bert;Integration;;+43 2;Graz\n" +
		";;;;\n" + // blank -> ignored
		";;bad@example.com;;\n" + // no name -> failed
		"Anna2;Integration;ANNA.IMP@example.com;;\n" // dup e-mail -> skipped

	w := httptest.NewRecorder()
	h.ImportPersons(w, csvUpload(t, csvData))
	if w.Code != http.StatusOK {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var res importResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Imported != 2 {
		t.Errorf("imported = %d, want 2", res.Imported)
	}
	if res.Skipped != 1 {
		t.Errorf("skipped (dup) = %d, want 1", res.Skipped)
	}
	if res.Failed != 1 {
		t.Errorf("failed = %d, want 1 (%v)", res.Failed, res.Errors)
	}
	var n int
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM persons WHERE last_name='Integration' AND lower(email)='anna.imp@example.com'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("persisted anna.imp rows = %d, want 1", n)
	}
}

// A plain comma CSV with English header aliases and no BOM also imports.
func TestImportPersonsCommaEnglish(t *testing.T) {
	h := testHandler(t)
	csvData := "first_name,last_name,email\nCarl,Integration,carl.imp@example.com\n"
	w := httptest.NewRecorder()
	h.ImportPersons(w, csvUpload(t, csvData))
	if w.Code != http.StatusOK {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var res importResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Imported != 1 {
		t.Errorf("imported = %d, want 1", res.Imported)
	}
}

// A file whose header has no recognizable columns is rejected with 400.
func TestImportPersonsUnknownHeader(t *testing.T) {
	h := testHandler(t)
	w := httptest.NewRecorder()
	h.ImportPersons(w, csvUpload(t, "alpha;beta;gamma\n1;2;3\n"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown header, got %d %s", w.Code, w.Body.String())
	}
}
