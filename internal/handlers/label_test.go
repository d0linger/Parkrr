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
