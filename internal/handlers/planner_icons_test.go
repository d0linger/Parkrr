package handlers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// iconReq baut die Multipart-Anfrage für Upload/Update: name und/oder Bild.
func iconReq(t *testing.T, method, path string, name string, imageBytes []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if name != "" {
		if err := mw.WriteField("name", name); err != nil {
			t.Fatalf("field: %v", err)
		}
	}
	if imageBytes != nil {
		fw, err := mw.CreateFormFile("file", "icon.jpg")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		if _, err := fw.Write(imageBytes); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	_ = mw.Close()
	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func mkIcon(t *testing.T, h *Handler, name string) int64 {
	t.Helper()
	rec := httptest.NewRecorder()
	h.UploadPlannerIcon(rec, iconReq(t, http.MethodPost, "/api/planner-icons", name, tinyJPEG(t)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload icon %s: %d %s", name, rec.Code, rec.Body.String())
	}
	var id int64
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT id FROM planner_icons WHERE name=$1`, name).Scan(&id); err != nil {
		t.Fatalf("icon id: %v", err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM planner_icons WHERE id=$1`, id) })
	return id
}

// Der Kernvertrag (Hundert 91): Upload → Umbenennen → Abruf → Löschen, und ein
// doppelter Name ist ein 409, kein 500.
func TestPlannerIconLifecycle(t *testing.T) {
	h := testHandler(t)
	name := "TestIcon-" + strconv.FormatInt(int64(len("x")), 10) + "-" + t.Name()
	id := mkIcon(t, h, name)

	// Doppelter Name → 409.
	rec := httptest.NewRecorder()
	h.UploadPlannerIcon(rec, iconReq(t, http.MethodPost, "/api/planner-icons", name, tinyJPEG(t)))
	if rec.Code != http.StatusConflict {
		t.Errorf("doppelter Name: %d, erwartet 409", rec.Code)
	}

	// Umbenennen behält die id — daran hängen die Fahrzeug-Verweise custom:<id>.
	renamed := name + "-neu"
	req := iconReq(t, http.MethodPut, "/api/planner-icons/"+strconv.FormatInt(id, 10), renamed, nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec = httptest.NewRecorder()
	h.UpdatePlannerIcon(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	var gotName string
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT name FROM planner_icons WHERE id=$1`, id).Scan(&gotName); err != nil {
		t.Fatalf("read: %v", err)
	}
	if gotName != renamed {
		t.Errorf("Name nach Umbenennen %q, erwartet %q", gotName, renamed)
	}

	// Abruf liefert die Bytes mit nosniff.
	greq := httptest.NewRequest(http.MethodGet, "/api/planner-icons/"+strconv.FormatInt(id, 10)+"/image", nil)
	greq.SetPathValue("id", strconv.FormatInt(id, 10))
	grec := httptest.NewRecorder()
	h.GetPlannerIcon(grec, greq)
	if grec.Code != http.StatusOK || grec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("get: %d, nosniff=%q", grec.Code, grec.Header().Get("X-Content-Type-Options"))
	}

	// Ein Update ganz ohne Änderung ist ein 400, kein stiller Erfolg.
	nreq := iconReq(t, http.MethodPut, "/api/planner-icons/"+strconv.FormatInt(id, 10), "", nil)
	nreq.SetPathValue("id", strconv.FormatInt(id, 10))
	nrec := httptest.NewRecorder()
	h.UpdatePlannerIcon(nrec, nreq)
	if nrec.Code != http.StatusBadRequest {
		t.Errorf("leeres Update: %d, erwartet 400", nrec.Code)
	}
}

// Das Löschen eines Icons muss die Fahrzeug-Verweise darauf MIT ausräumen: ein
// hängengebliebenes 'custom:<id>' ließe jedes spätere volle Fahrzeug-Update am
// Symbol-Validator scheitern.
func TestPlannerIconDeleteClearsVehicleReferences(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	id := mkIcon(t, h, "TestIcon-Del-"+t.Name())
	vid := mkPhotoVehicle(t, h)
	if _, err := h.Pool.Exec(ctx,
		`UPDATE vehicles SET planner_symbol=$1 WHERE id=$2`, "custom:"+strconv.FormatInt(id, 10), vid); err != nil {
		t.Fatalf("set symbol: %v", err)
	}

	dreq := httptest.NewRequest(http.MethodDelete, "/api/planner-icons/"+strconv.FormatInt(id, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(id, 10))
	drec := httptest.NewRecorder()
	h.DeletePlannerIcon(drec, dreq)
	if drec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", drec.Code, drec.Body.String())
	}

	var sym *string
	if err := h.Pool.QueryRow(ctx, `SELECT planner_symbol FROM vehicles WHERE id=$1`, vid).Scan(&sym); err != nil {
		t.Fatalf("read vehicle: %v", err)
	}
	if sym != nil {
		t.Errorf("der Fahrzeug-Verweis blieb hängen: %q", *sym)
	}
}

// Ein Nicht-Bild wird auch beim Icon-Upload abgelehnt — derselbe sanitizeImage-Weg
// wie bei den Fotos, aber der Handler muss ihn auch wirklich nehmen.
func TestPlannerIconUploadRejectsNonImage(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.UploadPlannerIcon(rec, iconReq(t, http.MethodPost, "/api/planner-icons", "Kaputt-"+t.Name(), []byte("kein bild")))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("Nicht-Bild: %d, erwartet 415", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "panic") {
		t.Error("Panik im Handler")
	}
}
