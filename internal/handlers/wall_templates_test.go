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

// TestDeleteWallTemplateRejectsBadID: DeleteWallTemplate parses the path id via pathID,
// so a zero, negative, or non-numeric id is a clean 400 before any DB work, matching the
// other {id} endpoints (PR #140/#141).
func TestDeleteWallTemplateRejectsBadID(t *testing.T) {
	h := testHandler(t)
	for _, bad := range []string{"0", "-1", "abc"} {
		req := httptest.NewRequest(http.MethodDelete, "/api/wall-templates/"+bad, nil)
		req.SetPathValue("id", bad)
		rec := httptest.NewRecorder()
		h.DeleteWallTemplate(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("delete wall-template with id %q: got %d, want 400", bad, rec.Code)
		}
	}
}
