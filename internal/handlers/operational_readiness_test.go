package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadinessOverall(t *testing.T) {
	tests := []struct {
		name  string
		items []readinessItem
		want  string
	}{
		{"ready", []readinessItem{{State: "ready"}}, "ready"},
		{"warning", []readinessItem{{State: "ready"}, {State: "warning"}}, "warning"},
		{"blocked wins", []readinessItem{{State: "warning"}, {State: "blocked"}}, "blocked"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := readinessOverall(test.items); got != test.want {
				t.Fatalf("readinessOverall() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestOperationalReadinessResponse(t *testing.T) {
	h := testHandler(t)
	recorder := httptest.NewRecorder()
	h.OperationalReadiness(recorder, httptest.NewRequest(http.MethodGet, "/api/admin/readiness", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response readinessResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 7 {
		t.Fatalf("items = %d, want 7", len(response.Items))
	}
	if response.Items[4].ID != "restore" {
		t.Fatalf("item 4 = %q, want restore right after backup", response.Items[4].ID)
	}
	if response.State != "blocked" {
		t.Fatalf("state = %q, want blocked for an unconfigured handler", response.State)
	}
}

func TestRestoreCheckItem(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-3 * 24 * time.Hour)
	stale := now.Add(-15 * 24 * time.Hour)
	tests := []struct {
		name       string
		configured bool
		tested     *time.Time
		state      string
		reason     string
	}{
		{"not configured", false, &fresh, "warning", "Erst prüfbar"},
		{"never tested", true, nil, "warning", "Noch keine Sicherung"},
		{"stale", true, &stale, "warning", "älter als 14 Tage"},
		{"fresh", true, &fresh, "ready", "entschlüsselt und geprüft"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := restoreCheckItem(tc.configured, tc.tested, now)
			if got.ID != "restore" || got.Href != "#/backup" {
				t.Fatalf("item = %+v", got)
			}
			if got.State != tc.state || !strings.Contains(got.Reason, tc.reason) {
				t.Fatalf("state/reason = %q/%q, want %q/…%q…", got.State, got.Reason, tc.state, tc.reason)
			}
		})
	}
}
