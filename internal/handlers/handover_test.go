package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func tinyPNGDataURL(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 6, 6))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func seedHandoverVehicle(t *testing.T, h *Handler) int64 {
	t.Helper()
	pid := createIntegrationPerson(t, h)
	catBody, _ := json.Marshal(map[string]any{
		"name": "Ho-Cat-" + strconv.FormatInt(time.Now().UnixNano(), 10), "default_monthly_cost": 10, "default_yearly_cost": 100,
	})
	crec := httptest.NewRecorder()
	h.CreateCategory(crec, httptest.NewRequest(http.MethodPost, "/api/categories", bytes.NewReader(catBody)))
	var cat struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(crec.Body.Bytes(), &cat)

	vBody, _ := json.Marshal(map[string]any{
		"person_id": pid, "category_id": cat.ID, "billing_period": "monthly",
		"status": "stored", "label": "Ho-Test", "start_date": "2024-01-01",
	})
	vrec := httptest.NewRecorder()
	h.CreateVehicle(vrec, httptest.NewRequest(http.MethodPost, "/api/vehicles", bytes.NewReader(vBody)))
	if vrec.Code != http.StatusCreated {
		t.Fatalf("create vehicle: %d %s", vrec.Code, vrec.Body.String())
	}
	var veh struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(vrec.Body.Bytes(), &veh)
	return veh.ID
}

func TestHandoverProtocolLifecycle(t *testing.T) {
	h := testHandler(t)
	vid := seedHandoverVehicle(t, h)
	vids := strconv.FormatInt(vid, 10)

	// Create with a real PNG signature.
	body, _ := json.Marshal(map[string]any{
		"direction": "einlagerung", "notes": "Kratzer vorne links.",
		"signer_name": "Max Mustermann", "signature": tinyPNGDataURL(t),
	})
	creq := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+vids+"/handovers", bytes.NewReader(body))
	creq.SetPathValue("id", vids)
	cw := httptest.NewRecorder()
	h.CreateHandover(cw, creq)
	if cw.Code != http.StatusCreated {
		t.Fatalf("create handover: %d %s", cw.Code, cw.Body.String())
	}
	var m handoverMeta
	if err := json.Unmarshal(cw.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if !m.HasSignature || m.Direction != "einlagerung" {
		t.Errorf("unexpected meta: %+v", m)
	}

	// List returns it.
	lreq := httptest.NewRequest(http.MethodGet, "/api/vehicles/"+vids+"/handovers", nil)
	lreq.SetPathValue("id", vids)
	lw := httptest.NewRecorder()
	h.ListHandovers(lw, lreq)
	var list []handoverMeta
	_ = json.Unmarshal(lw.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}

	hid := strconv.FormatInt(m.ID, 10)

	// Signature image.
	sreq := httptest.NewRequest(http.MethodGet, "/api/handovers/"+hid+"/signature", nil)
	sreq.SetPathValue("id", hid)
	sw := httptest.NewRecorder()
	h.GetHandoverSignature(sw, sreq)
	if sw.Code != http.StatusOK || sw.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("signature: %d %q", sw.Code, sw.Header().Get("Content-Type"))
	}
	if !bytes.HasPrefix(sw.Body.Bytes(), []byte("\x89PNG")) {
		t.Error("signature body is not PNG")
	}

	// PDF.
	preq := httptest.NewRequest(http.MethodGet, "/api/handovers/"+hid+"/pdf", nil)
	preq.SetPathValue("id", hid)
	pw := httptest.NewRecorder()
	h.HandoverPDF(pw, preq)
	if pw.Code != http.StatusOK || !bytes.HasPrefix(pw.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("pdf: %d prefix=%q", pw.Code, pw.Body.Bytes()[:min(6, pw.Body.Len())])
	}

	// Invalid direction → 400.
	bad, _ := json.Marshal(map[string]any{"direction": "sideways"})
	breq := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+vids+"/handovers", bytes.NewReader(bad))
	breq.SetPathValue("id", vids)
	bw := httptest.NewRecorder()
	h.CreateHandover(bw, breq)
	if bw.Code != http.StatusBadRequest {
		t.Errorf("bad direction: want 400, got %d", bw.Code)
	}

	// Delete.
	dreq := httptest.NewRequest(http.MethodDelete, "/api/handovers/"+hid, nil)
	dreq.SetPathValue("id", hid)
	dw := httptest.NewRecorder()
	h.DeleteHandover(dw, dreq)
	if dw.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", dw.Code, dw.Body.String())
	}
	lw2 := httptest.NewRecorder()
	lreq2 := httptest.NewRequest(http.MethodGet, "/api/vehicles/"+vids+"/handovers", nil)
	lreq2.SetPathValue("id", vids)
	h.ListHandovers(lw2, lreq2)
	var list2 []handoverMeta
	_ = json.Unmarshal(lw2.Body.Bytes(), &list2)
	if len(list2) != 0 {
		t.Errorf("after delete, list len = %d, want 0", len(list2))
	}
}
