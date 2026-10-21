package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// variantJPEG liefert bei jedem Aufruf ein anderes Bild: das PDF legt identische
// Bilder zu einem Objekt zusammen, der Vergleich braucht zwei verschiedene.
var jpegSeed uint8

func variantJPEG(t *testing.T) []byte {
	t.Helper()
	jpegSeed++
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for x := 0; x < 8; x++ {
		img.Set(x, 2, color.RGBA{R: jpegSeed * 7, G: 255 - jpegSeed, B: 90, A: 255})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadHandoverPhoto(t *testing.T, h *Handler, handoverID int64, view string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("view", view)
	fw, _ := mw.CreateFormFile("photo", view+".jpg")
	_, _ = fw.Write(variantJPEG(t))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/handovers/"+strconv.FormatInt(handoverID, 10)+"/photos", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetPathValue("id", strconv.FormatInt(handoverID, 10))
	w := httptest.NewRecorder()
	h.UploadHandoverPhoto(w, req)
	return w
}

func createHandover(t *testing.T, h *Handler, vid int64, body string) handoverMeta {
	t.Helper()
	w := callJSONRaw(h, h.CreateHandover, http.MethodPost, "/api/vehicles/x/handovers", strconv.FormatInt(vid, 10), body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create handover %s: %d %s", body, w.Code, w.Body.String())
	}
	var m handoverMeta
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return m
}

func callJSONRaw(h *Handler, hf http.HandlerFunc, method, path, id, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if id != "" {
		r.SetPathValue("id", id)
	}
	rec := httptest.NewRecorder()
	hf(rec, r)
	return rec
}

// Ausbau 24: Fotos hängen am Protokoll (nicht in der Galerie), feste Ansichten, eine
// Schadenliste, nur kurz nach dem Protokoll ergänzbar, einzeln nicht löschbar. Das PDF
// der Abholung stellt die Einlagerung daneben.
func TestHandoverConditionPhotos(t *testing.T) {
	h := testHandler(t)
	_, vid, _ := seasonFixture(t, h, "")
	base := time.Now()
	h.Now = func() time.Time { return base }

	in := createHandover(t, h, vid, `{"direction":"einlagerung","damages":[{"area":"vorne links","note":"Kratzer 10 cm"},{"area":" ","note":""}]}`)
	if len(in.Damages) != 1 || in.Damages[0].Area != "vorne links" || !in.PhotosOpen {
		t.Fatalf("damages/open after create: %+v", in)
	}
	if w := uploadHandoverPhoto(t, h, in.ID, "vorne"); w.Code != http.StatusCreated {
		t.Fatalf("upload vorne: %d %s", w.Code, w.Body.String())
	}
	if w := uploadHandoverPhoto(t, h, in.ID, "dach"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown view: %d, want 400", w.Code)
	}

	gallery := callJSON(h, h.ListPhotos, http.MethodGet, "/api/vehicles/x/photos", strconv.FormatInt(vid, 10), nil)
	if strings.TrimSpace(gallery.Body.String()) != "[]" {
		t.Fatalf("gallery must not show protocol photos: %s", gallery.Body.String())
	}
	list := callJSON(h, h.ListHandovers, http.MethodGet, "/api/vehicles/x/handovers", strconv.FormatInt(vid, 10), nil)
	var hs []handoverMeta
	_ = json.Unmarshal(list.Body.Bytes(), &hs)
	if len(hs) != 1 || len(hs[0].Photos) != 1 || hs[0].Photos[0].View != "vorne" {
		t.Fatalf("list handovers: %s", list.Body.String())
	}
	del := callJSON(h, h.DeletePhoto, http.MethodDelete, "/api/photos/x", strconv.FormatInt(hs[0].Photos[0].ID, 10), nil)
	if del.Code != http.StatusConflict {
		t.Fatalf("delete protocol photo alone: %d, want 409", del.Code)
	}

	out := createHandover(t, h, vid, `{"direction":"auslagerung","damages":[{"area":"Heck","note":"Delle"}]}`)
	if w := uploadHandoverPhoto(t, h, out.ID, "vorne"); w.Code != http.StatusCreated {
		t.Fatalf("upload auslagerung: %d %s", w.Code, w.Body.String())
	}
	pdf := callJSON(h, h.HandoverPDF, http.MethodGet, "/api/handovers/x/pdf", strconv.FormatInt(out.ID, 10), nil)
	if pdf.Code != http.StatusOK || !bytes.HasPrefix(pdf.Body.Bytes(), []byte("%PDF")) {
		t.Fatalf("pdf: %d %q", pdf.Code, pdf.Body.String()[:min(40, pdf.Body.Len())])
	}
	if n := bytes.Count(pdf.Body.Bytes(), []byte("/Subtype /Image")); n < 2 {
		t.Errorf("pdf embeds %d images, want both the Einlagerung and the Abholung photo", n)
	}

	// Höchstens 12 je Protokoll.
	for i := 1; i < maxHandoverPhotos; i++ {
		if w := uploadHandoverPhoto(t, h, out.ID, "schaden"); w.Code != http.StatusCreated {
			t.Fatalf("upload %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if w := uploadHandoverPhoto(t, h, out.ID, "schaden"); w.Code != http.StatusConflict {
		t.Fatalf("13th photo: %d, want 409", w.Code)
	}
	// Nach 24 Stunden ist der Satz geschlossen.
	h.Now = func() time.Time { return base.Add(25 * time.Hour) }
	if w := uploadHandoverPhoto(t, h, in.ID, "hinten"); w.Code != http.StatusConflict {
		t.Fatalf("late upload: %d, want 409", w.Code)
	}
	if _, err := h.Pool.Exec(context.Background(), `UPDATE handover_protocols SET damages = '[]' WHERE id = $1`, in.ID); err == nil {
		t.Fatal("damages must be immutable like the rest of the protocol")
	}
}

// Ausbau 25: fällig nach Intervall je Gefährt, Erledigen mit einem Tipp, auf Wunsch mit
// Position über CreateCharge; zweimal am selben Tag geht nicht; Ladebedarf ohne Aufgabe
// wird gemeldet.
func TestServiceTasks(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, vid, catID := seasonFixture(t, h, "")
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	var svcID int64
	if err := h.Pool.QueryRow(ctx, `INSERT INTO service_types (name, default_amount) VALUES ($1, 15) RETURNING id`, "Laden-"+tag).Scan(&svcID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM service_tasks WHERE title LIKE '%'||$1`, tag)
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM service_types WHERE id = $1`, svcID)
	})
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET needs_power = true WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	h.Now = func() time.Time { return time.Now() }
	due := func(days int) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServiceTasksDue(w, httptest.NewRequest(http.MethodGet, "/api/service-tasks/due?days="+strconv.Itoa(days), nil))
		if w.Code != http.StatusOK {
			t.Fatalf("due: %d %s", w.Code, w.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	if n := due(0)["power_without_task"].(float64); n < 1 {
		t.Fatalf("power suggestion: %v, want at least our vehicle", n)
	}
	if w := callJSONRaw(h, h.CreateServiceTask, http.MethodPost, "/api/service-tasks", "", `{"title":"x","scope":"category","interval_days":30}`); w.Code != http.StatusBadRequest {
		t.Fatalf("category without id: %d, want 400", w.Code)
	}
	w := callJSONRaw(h, h.CreateServiceTask, http.MethodPost, "/api/service-tasks", "",
		`{"title":"Erhaltungsladung prüfen `+tag+`","scope":"category","category_id":`+strconv.FormatInt(catID, 10)+`,"interval_days":30,"service_type_id":`+strconv.FormatInt(svcID, 10)+`}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var task serviceTask
	_ = json.Unmarshal(w.Body.Bytes(), &task)
	findDue := func(m map[string]any) map[string]any {
		for _, it := range m["items"].([]any) {
			d := it.(map[string]any)
			if int64(d["task_id"].(float64)) == task.ID && int64(d["vehicle_id"].(float64)) == vid {
				return d
			}
		}
		return nil
	}
	if findDue(due(7)) != nil {
		t.Fatal("a new task is first due one interval after it was created")
	}
	h.Now = func() time.Time { return time.Now().AddDate(0, 0, 31) }
	d := findDue(due(0))
	if d == nil || d["service_amount"].(float64) != 15 {
		t.Fatalf("due after 31 days: %v", d)
	}
	tid := strconv.FormatInt(task.ID, 10)
	done := callJSONRaw(h, h.CompleteServiceTask, http.MethodPost, "/api/service-tasks/x/done", tid,
		`{"vehicle_id":`+strconv.FormatInt(vid, 10)+`,"bill":true}`)
	if done.Code != http.StatusCreated {
		t.Fatalf("done: %d %s", done.Code, done.Body.String())
	}
	var res struct {
		ID       int64  `json:"id"`
		ChargeID *int64 `json:"charge_id"`
	}
	_ = json.Unmarshal(done.Body.Bytes(), &res)
	var desc string
	var amount float64
	var owner int64
	if res.ChargeID == nil {
		t.Fatal("bill:true must create a charge")
	}
	if err := h.Pool.QueryRow(ctx, `SELECT description, amount, person_id FROM charges WHERE id = $1`, *res.ChargeID).Scan(&desc, &amount, &owner); err != nil {
		t.Fatal(err)
	}
	if amount != 15 || owner != pid || !strings.Contains(desc, "Laden-"+tag) {
		t.Errorf("charge: %q %v person %d", desc, amount, owner)
	}
	if again := callJSONRaw(h, h.CompleteServiceTask, http.MethodPost, "/api/service-tasks/x/done", tid,
		`{"vehicle_id":`+strconv.FormatInt(vid, 10)+`}`); again.Code != http.StatusConflict {
		t.Fatalf("same day twice: %d, want 409", again.Code)
	}
	if findDue(due(0)) != nil {
		t.Fatal("done today, so not due again until the next interval")
	}
	undo := callJSON(h, h.UndoServiceTask, http.MethodDelete, "/api/service-task-log/x", strconv.FormatInt(res.ID, 10), nil)
	if undo.Code != http.StatusOK || !strings.Contains(undo.Body.String(), `"charge_id":`+strconv.FormatInt(*res.ChargeID, 10)) {
		t.Fatalf("undo: %d %s", undo.Code, undo.Body.String())
	}
	if findDue(due(0)) == nil {
		t.Fatal("after undo the task is due again")
	}
	// Eine Aufgabe für ein anderes Gefährt gilt hier nicht.
	_, other, _ := seasonFixture(t, h, "")
	w = callJSONRaw(h, h.CreateServiceTask, http.MethodPost, "/api/service-tasks", "",
		`{"title":"Reifen `+tag+`","scope":"vehicle","vehicle_id":`+strconv.FormatInt(other, 10)+`,"interval_days":14}`)
	var t2 serviceTask
	_ = json.Unmarshal(w.Body.Bytes(), &t2)
	if c := callJSONRaw(h, h.CompleteServiceTask, http.MethodPost, "/api/service-tasks/x/done", strconv.FormatInt(t2.ID, 10),
		`{"vehicle_id":`+strconv.FormatInt(vid, 10)+`}`).Code; c != http.StatusBadRequest {
		t.Fatalf("task for another vehicle: %d, want 400", c)
	}
}

// Ausbau 34: ausgeben, nicht doppelt, zurücknehmen; eine Person mit offenem Schlüssel
// lässt sich nicht löschen; ein je ausgegebenes Zugangsmittel nur ausmustern.
func TestAccessMediaBook(t *testing.T) {
	h := testHandler(t)
	pid, _, _ := seasonFixture(t, h, "")
	other := createIntegrationPerson(t, h)
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM access_media WHERE label LIKE '%'||$1`, tag)
	})

	w := callJSONRaw(h, h.CreateAccessMedium, http.MethodPost, "/api/access-media", "", `{"kind":"handsender","label":"7-`+tag+`"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var m accessMedium
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	mid := strconv.FormatInt(m.ID, 10)
	if c := callJSONRaw(h, h.CreateAccessMedium, http.MethodPost, "/api/access-media", "", `{"kind":"handsender","label":"7-`+tag+`"}`).Code; c != http.StatusConflict {
		t.Fatalf("duplicate: %d, want 409", c)
	}
	if c := callJSONRaw(h, h.IssueAccessMedium, http.MethodPost, "/api/access-media/x/issue", mid, `{"person_id":`+strconv.FormatInt(pid, 10)+`}`).Code; c != http.StatusCreated {
		t.Fatalf("issue: %d", c)
	}
	if c := callJSONRaw(h, h.IssueAccessMedium, http.MethodPost, "/api/access-media/x/issue", mid, `{"person_id":`+strconv.FormatInt(other, 10)+`}`).Code; c != http.StatusConflict {
		t.Fatalf("issue twice: %d, want 409", c)
	}
	open := callJSON(h, h.PersonAccessMedia, http.MethodGet, "/api/persons/x/access-media", strconv.FormatInt(pid, 10), nil)
	if !strings.Contains(open.Body.String(), "7-"+tag) {
		t.Fatalf("person loans: %s", open.Body.String())
	}
	if c := callJSONRaw(h, h.UpdateAccessMedium, http.MethodPut, "/api/access-media/x", mid, `{"kind":"handsender","label":"7-`+tag+`","retired":true}`).Code; c != http.StatusConflict {
		t.Fatalf("retire while issued: %d, want 409", c)
	}
	// Ohne offenes Zugangsmittel (und ohne Zahlungen) lässt sich eine Person löschen.
	delP := callJSON(h, h.DeletePerson, http.MethodDelete, "/api/persons/x", strconv.FormatInt(other, 10), nil)
	if delP.Code != http.StatusOK {
		t.Fatalf("delete person without loans: %d %s", delP.Code, delP.Body.String())
	}
	if c := callJSONRaw(h, h.ReturnAccessMedium, http.MethodPost, "/api/access-media/x/return", mid, `{}`).Code; c != http.StatusOK {
		t.Fatalf("return: %d", c)
	}
	if c := callJSONRaw(h, h.ReturnAccessMedium, http.MethodPost, "/api/access-media/x/return", mid, `{}`).Code; c != http.StatusConflict {
		t.Fatalf("return twice: %d, want 409", c)
	}
	if c := callJSON(h, h.DeleteAccessMedium, http.MethodDelete, "/api/access-media/x", mid, nil).Code; c != http.StatusConflict {
		t.Fatalf("delete with history: %d, want 409", c)
	}
	hist := callJSON(h, h.AccessMediumLoans, http.MethodGet, "/api/access-media/x/loans", mid, nil)
	var loans []accessLoan
	_ = json.Unmarshal(hist.Body.Bytes(), &loans)
	if len(loans) != 1 || loans[0].ReturnedOn == nil {
		t.Fatalf("history: %s", hist.Body.String())
	}

	// Offene Ausgabe blockiert das Löschen der Person.
	third := createIntegrationPerson(t, h)
	w = callJSONRaw(h, h.CreateAccessMedium, http.MethodPost, "/api/access-media", "", `{"kind":"schluessel","label":"S-`+tag+`"}`)
	var k accessMedium
	_ = json.Unmarshal(w.Body.Bytes(), &k)
	_ = callJSONRaw(h, h.IssueAccessMedium, http.MethodPost, "/api/access-media/x/issue", strconv.FormatInt(k.ID, 10), `{"person_id":`+strconv.FormatInt(third, 10)+`}`)
	blocked := callJSON(h, h.DeletePerson, http.MethodDelete, "/api/persons/x", strconv.FormatInt(third, 10), nil)
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "S-"+tag) {
		t.Fatalf("delete person with open key: %d %s", blocked.Code, blocked.Body.String())
	}
}
