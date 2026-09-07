package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// TestWallTemplateTrimIsAudited: der Ringpuffer verdrängt jenseits von
// maxWallTemplates die ältesten Vorlagen. Das lief als stilles `_, _ =`, sodass
// Vorlagen spurlos verschwanden (Hundert API-84). Jetzt muss die Verdrängung eine
// Audit-Zeile hinterlassen.
func TestWallTemplateTrimIsAudited(t *testing.T) {
	h := testHandler(t)
	// Tabelle leeren, damit der Test unabhängig vom Vorzustand zählt.
	if _, err := h.Pool.Exec(t.Context(), `DELETE FROM wall_templates`); err != nil {
		t.Fatalf("clear templates: %v", err)
	}
	mk := func(name string) {
		body, _ := json.Marshal(map[string]any{"name": name, "walls": map[string]any{"nodes": []any{}, "edges": []any{}}})
		rec := httptest.NewRecorder()
		h.CreateWallTemplate(rec, httptest.NewRequest(http.MethodPost, "/api/wall-templates", bytes.NewReader(body)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// Die ÄLTESTE ist "Verdraengt-0" und fliegt beim (maxWallTemplates+1)-ten raus.
	for i := 0; i <= maxWallTemplates; i++ {
		mk(fmt.Sprintf("Verdraengt-%d", i))
	}
	var n int
	if err := h.Pool.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_log
		  WHERE entity='wall_template' AND action='delete' AND summary LIKE '%Verdraengt-0'`).Scan(&n); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if n == 0 {
		t.Error("die verdrängte Vorlage muss eine Audit-Zeile hinterlassen, sonst verschwindet sie spurlos")
	}
	var total int
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM wall_templates`).Scan(&total); err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if total > maxWallTemplates {
		t.Errorf("Ringpuffer hält %d Vorlagen, erlaubt sind %d", total, maxWallTemplates)
	}
}
