package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Exercises the printable vehicle label: HTML output with an embedded QR, correct
// HTML-escaping of user fields (the relaxed per-page CSP must not become an XSS),
// the tightened CSP header, and 404 for a missing vehicle. Runs only when
// PARKRR_TEST_DATABASE_URL is set.
func TestVehicleLabel(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)

	catName := "Label-Cat-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cbody, _ := json.Marshal(map[string]any{"name": catName, "default_monthly_cost": 30, "default_yearly_cost": 300})
	crec := httptest.NewRecorder()
	h.CreateCategory(crec, httptest.NewRequest(http.MethodPost, "/api/categories", bytes.NewReader(cbody)))
	var cat struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(crec.Body.Bytes(), &cat)

	vbody, _ := json.Marshal(map[string]any{
		"person_id": pid, "category_id": cat.ID, "billing_period": "monthly",
		"status": "stored", "label": "Bulli <b>X</b>", "start_date": "2024-01-01",
	})
	vrec := httptest.NewRecorder()
	h.CreateVehicle(vrec, httptest.NewRequest(http.MethodPost, "/api/vehicles", bytes.NewReader(vbody)))
	if vrec.Code != http.StatusCreated {
		t.Fatalf("create vehicle: %d %s", vrec.Code, vrec.Body.String())
	}
	var veh struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(vrec.Body.Bytes(), &veh)

	req := httptest.NewRequest(http.MethodGet, "/api/vehicles/"+strconv.FormatInt(veh.ID, 10)+"/label", nil)
	req.SetPathValue("id", strconv.FormatInt(veh.ID, 10))
	req.Host = "parkrr.example.com"
	w := httptest.NewRecorder()
	h.VehicleLabel(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("label: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("content-type %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(body, "data:image/png;base64,") {
		t.Error("missing embedded QR data URI")
	}
	// XSS: the user-supplied label must be escaped, not rendered as markup.
	if strings.Contains(body, "Bulli <b>X</b>") {
		t.Error("label was NOT HTML-escaped — XSS risk under the relaxed CSP")
	}
	if !strings.Contains(body, "Bulli &lt;b&gt;X&lt;/b&gt;") {
		t.Error("escaped label missing from output")
	}
	// The per-page CSP must be tight (no 'self', img only data:).
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src data:") || strings.Contains(csp, "'self'") {
		t.Errorf("unexpected label CSP: %q", csp)
	}

	// Unknown vehicle -> 404.
	req2 := httptest.NewRequest(http.MethodGet, "/api/vehicles/99999999/label", nil)
	req2.SetPathValue("id", "99999999")
	w2 := httptest.NewRecorder()
	h.VehicleLabel(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("unknown vehicle: want 404, got %d", w2.Code)
	}
}

// The A4 sheet lists every placed, active Gefährt of the hall, escaped like the
// single label and under the same tight CSP; an unknown hall is a 404.
func TestHallLabels(t *testing.T) {
	h := testHandler(t)
	ctx := t.Context()
	pid := createIntegrationPerson(t, h)
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	var catID, garageID, hallID, spotID int64
	if err := h.Pool.QueryRow(ctx, `INSERT INTO categories (name, default_monthly_cost, default_yearly_cost) VALUES ($1, 20, 200) RETURNING id`, "Sheet-"+tag).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	if err := h.Pool.QueryRow(ctx, `INSERT INTO garages (name) VALUES ($1) RETURNING id`, "Sheet-Garage-"+tag).Scan(&garageID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(t.Context(), `UPDATE vehicles SET spot_id=NULL WHERE person_id=$1`, pid)
		_, _ = h.Pool.Exec(t.Context(), `DELETE FROM garages WHERE id=$1`, garageID)
	})
	if err := h.Pool.QueryRow(ctx, `INSERT INTO halls (garage_id, name) VALUES ($1, 'Halle <A>') RETURNING id`, garageID).Scan(&hallID); err != nil {
		t.Fatal(err)
	}
	if err := h.Pool.QueryRow(ctx, `INSERT INTO spots (hall_id, label, geometry) VALUES ($1, 'B-07', '{"x":0,"y":0,"w":4,"h":2}') RETURNING id`, hallID).Scan(&spotID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO vehicles (person_id, category_id, label, license_plate, billing_period, rate, start_date, status, spot_id)
		VALUES ($1, $2, '<i>Bulli</i>', 'W-1', 'monthly', 20, '2026-01-01', 'stored', $3)`, pid, catID, spotID); err != nil {
		t.Fatal(err)
	}

	call := func(id int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/halls/"+strconv.FormatInt(id, 10)+"/labels", nil)
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		req.Host = "parkrr.example.com"
		w := httptest.NewRecorder()
		h.HallLabels(w, req)
		return w
	}
	w := call(hallID)
	if w.Code != http.StatusOK {
		t.Fatalf("labels: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"&lt;i&gt;Bulli&lt;/i&gt;", "Halle &lt;A&gt; · 1 Etiketten", "Platz B-07", "data:image/png;base64,", "@page { size: A4"} {
		if !strings.Contains(body, want) {
			t.Errorf("sheet is missing %q", want)
		}
	}
	if strings.Contains(body, "<i>Bulli</i>") {
		t.Error("vehicle label rendered unescaped")
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	if w := call(987654321987); w.Code != http.StatusNotFound {
		t.Errorf("unknown hall: %d, want 404", w.Code)
	}
}
