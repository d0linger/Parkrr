package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWallTemplateWallsTooLarge: an over-cap walls blob is rejected (400), matching the
// hall/spot geometry endpoints, even though the request body is under the 1 MiB decode limit.
func TestWallTemplateWallsTooLarge(t *testing.T) {
	h := testHandler(t)
	big := `{"x":"` + strings.Repeat("a", maxGeometryLen+10) + `"}`
	body, _ := json.Marshal(map[string]any{"name": "TPL", "walls": json.RawMessage(big)})
	rec := httptest.NewRecorder()
	h.CreateWallTemplate(rec, httptest.NewRequest(http.MethodPost, "/api/wall-templates", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized walls should be 400, got %d %s", rec.Code, rec.Body.String())
	}
}

// TestWallTemplateCreateOK: a small valid template is accepted.
func TestWallTemplateCreateOK(t *testing.T) {
	h := testHandler(t)
	body, _ := json.Marshal(map[string]any{"name": "Halle A", "walls": map[string]any{"nodes": []any{}, "edges": []any{}}})
	rec := httptest.NewRecorder()
	h.CreateWallTemplate(rec, httptest.NewRequest(http.MethodPost, "/api/wall-templates", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("valid template should be 201 Created, got %d %s", rec.Code, rec.Body.String())
	}
}
