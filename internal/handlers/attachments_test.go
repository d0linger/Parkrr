package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func uploadAttachment(t *testing.T, h *Handler, path, id, filename string, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if _, err := fw.Write(payload); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.UploadAttachment(rec, req)
	return rec
}

// Anhänge (Hundert 57): PDF per Magic-Number, Bilder über die Foto-Pipeline,
// alles andere abgelehnt — und die Auslieferung ist ein erzwungener Download.
func TestAttachmentLifecycle(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	pids := strconv.FormatInt(pid, 10)

	// Ein Mini-PDF (Magic-Number reicht der Prüfung; der Inhalt ist opak).
	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF")
	rec := uploadAttachment(t, h, "/api/persons/"+pids+"/attachments", pids, "vertrag.pdf", pdf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("pdf upload: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"content_type":"application/pdf"`) {
		t.Errorf("PDF-Typ fehlt: %s", rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// Ein Bild läuft durch sanitizeImage und wird als solches gespeichert.
	if rec := uploadAttachment(t, h, "/api/persons/"+pids+"/attachments", pids, "foto.jpg", tinyJPEG(t)); rec.Code != http.StatusCreated {
		t.Errorf("jpeg upload: %d %s", rec.Code, rec.Body.String())
	}
	// Eine .exe mit PDF-Dateinamen fällt am INHALT durch, nicht am Namen.
	if rec := uploadAttachment(t, h, "/api/persons/"+pids+"/attachments", pids, "harmlos.pdf", []byte("MZ\x90\x00 kein pdf")); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("exe als pdf: %d, erwartet 415", rec.Code)
	}

	// Liste zeigt Metadaten, nie Bytes.
	lreq := httptest.NewRequest(http.MethodGet, "/api/persons/"+pids+"/attachments", nil)
	lreq.SetPathValue("id", pids)
	lrec := httptest.NewRecorder()
	h.ListAttachments(lrec, lreq)
	if lrec.Code != http.StatusOK || !strings.Contains(lrec.Body.String(), "vertrag.pdf") {
		t.Fatalf("list: %d %s", lrec.Code, lrec.Body.String())
	}

	// Download: erzwungener Download mit nosniff — ein PDF kann Skripte tragen,
	// als Attachment läuft keines im Kontext der Anwendung.
	greq := httptest.NewRequest(http.MethodGet, "/api/attachments/"+strconv.FormatInt(created.ID, 10), nil)
	greq.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	grec := httptest.NewRecorder()
	h.GetAttachment(grec, greq)
	if grec.Code != http.StatusOK {
		t.Fatalf("get: %d", grec.Code)
	}
	if cd := grec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("kein erzwungener Download: %q", cd)
	}
	if grec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff fehlt")
	}
	if !bytes.Equal(grec.Body.Bytes(), pdf) {
		t.Error("PDF-Bytes verändert — PDFs sind opak und müssen unverändert zurückkommen")
	}

	// Löschen, doppelt = 404.
	dreq := httptest.NewRequest(http.MethodDelete, "/api/attachments/"+strconv.FormatInt(created.ID, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	drec := httptest.NewRecorder()
	h.DeleteAttachment(drec, dreq)
	if drec.Code != http.StatusOK {
		t.Fatalf("delete: %d", drec.Code)
	}
	drec2 := httptest.NewRecorder()
	h.DeleteAttachment(drec2, dreq)
	if drec2.Code != http.StatusNotFound {
		t.Errorf("zweites Löschen: %d, erwartet 404", drec2.Code)
	}
}

