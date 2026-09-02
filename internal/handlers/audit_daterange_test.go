package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The audit-log date range (?from/?to) filters by day and ignores malformed
// dates. Runs only when PARKRR_TEST_DATABASE_URL is set.
func TestListAuditDateFilter(t *testing.T) {
	h := testHandler(t)
	createIntegrationPerson(t, h) // writes a create/person audit entry, dated now

	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	get := func(qs string) []map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/audit?"+qs, nil)
		w := httptest.NewRecorder()
		h.ListAudit(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("audit ?%s: %d %s", qs, w.Code, w.Body.String())
		}
		var out []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode ?%s: %v", qs, err)
		}
		return out
	}

	if n := len(get("entity=person&from=" + today + "&to=" + today)); n == 0 {
		t.Error("expected at least the person-create entry within today's range")
	}
	if n := len(get("from=" + tomorrow)); n != 0 {
		t.Errorf("expected no entries dated from tomorrow, got %d", n)
	}
	if n := len(get("from=not-a-date")); n == 0 {
		t.Error("a malformed 'from' must be ignored (no filter), still returning entries")
	}

	// Over-long query parameters must be safely capped and not cause errors.
	longQ := "q=" + strings.Repeat("a", maxSearchQueryLen+100)
	req := httptest.NewRequest(http.MethodGet, "/api/audit?"+longQ, nil)
	w := httptest.NewRecorder()
	h.ListAudit(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for over-long search query parameter, got %d", w.Code)
	}
}
