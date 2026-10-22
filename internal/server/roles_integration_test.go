//go:build integration

package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/mail"
)

// Rollen (Ausbau 35): jede registrierte API-Route, jede Rolle — erlaubt oder 403.
// Die Erwartung steht hier unabhängig von server.go: wer eine Route falsch einhängt
// (oder eine neue mit Beträgen als authed), bekommt hier die Abweichung genannt.

// Nur Admins (außer Steuerbereich, der hat eine eigene Regel).
var adminOnlyRoutes = set(
	"DELETE /api/users/{id}", "GET /api/users", "POST /api/users", "POST /api/users/{id}/reset-2fa", "PUT /api/users/{id}",
	"GET /api/audit", "GET /api/audit/export",
	"GET /api/admin/readiness", "GET /api/help/handbook", "GET /api/mail-log",
	"GET /api/billing/settings", "POST /api/billing/settings",
	"GET /api/mail-automation", "PUT /api/mail-automation",
	"GET /api/backup/file/{name}", "GET /api/backup/s3/file/{name}", "GET /api/backup/status",
	"POST /api/backup", "POST /api/backup/restore", "POST /api/backup/restore-s3", "POST /api/backup/run",
	"POST /api/backup/s3", "POST /api/backup/s3/test", "POST /api/backup/schedule", "POST /api/backup/validate", "POST /api/backup/validate-s3",
	"GET /api/persons/{id}/export", "POST /api/contract-template",
	"DELETE /api/handovers/{id}", "DELETE /api/deposits/{id}",
)

// Lesen nur für Bearbeiter (Arbeitslisten des Büros).
var editorReads = set(
	"GET /api/backup/health", "GET /api/import/persons/template", "GET /api/import/vehicles/template",
	"GET /api/invoices/preview", "GET /api/mail-outbox", "GET /api/persons/{id}/portal-links",
	"GET /api/pickup-windows", "GET /api/portal-requests", "GET /api/waitlist",
)

// Schreibwege, die jede angemeldete Rolle für sich selbst hat.
var ownWrites = set("POST /api/calendar/feed", "DELETE /api/calendar/feed", "POST /api/client-error")

// Die Halle: Bearbeiter und Hallendienst.
var hallWrites = set(
	"POST /api/vehicles/{id}/handovers", "POST /api/handovers/{id}/photos", "POST /api/vehicles/{id}/photos",
	"PUT /api/vehicles/{id}/photos/order", "DELETE /api/photos/{id}", "POST /api/halls/{id}/spots", "PUT /api/spots/{id}",
	"DELETE /api/spots/{id}", "PUT /api/spots/{id}/vehicle", "DELETE /api/spots/{id}/vehicle", "PUT /api/halls/{id}/spots/geometry",
	"PUT /api/vehicles/{id}/planner", "PUT /api/vehicles/{id}/dimensions", "POST /api/service-tasks/{id}/done",
	"DELETE /api/service-task-log/{id}", "POST /api/access-media/{id}/issue", "POST /api/access-media/{id}/return",
	"POST /api/vehicle-pauses/{id}/return",
)

// Lesen mit Beträgen: jede Rolle außer dem Hallendienst.
var moneyReads = set(
	"GET /api/bank-transactions", "GET /api/charges", "GET /api/cpi", "GET /api/indexation", "GET /api/indexation/preview",
	"GET /api/export/{entity}", "GET /api/invoices", "GET /api/invoices/overdue", "GET /api/invoices/{id}",
	"GET /api/invoices/{id}/mailings", "GET /api/invoices/{id}/pay-qr", "GET /api/invoices/{id}/pdf", "GET /api/overview",
	"GET /api/overview/forecast", "GET /api/persons/outstanding", "GET /api/persons/{id}/agreements", "GET /api/persons/{id}/deposits",
	"GET /api/persons/{id}/invoices", "GET /api/persons/{id}/open-items", "GET /api/persons/{id}/payments",
	"GET /api/persons/{id}/recurring", "GET /api/persons/{id}/stats", "GET /api/persons/{id}/timeline",
	"GET /api/reports/outstanding.pdf", "GET /api/vehicles/{id}/rate-changes", "GET /api/vehicles/{id}/contract.pdf",
	"GET /api/vehicles/{id}/pause-preview",
)

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

func isTaxRoute(path string) bool {
	return strings.HasPrefix(path, "/api/tax/") || strings.HasPrefix(path, "/api/reports/tax-")
}

// allowed: darf die Rolle die Route erreichen (nicht vom Wrapper abgewiesen)?
func allowed(role, pattern string) bool {
	method, path, _ := strings.Cut(pattern, " ")
	switch {
	case adminOnlyRoutes[pattern]:
		return false
	case isTaxRoute(path):
		return role == "buchhaltung"
	case hallWrites[pattern]:
		return role == "editor" || role == "hallendienst"
	case moneyReads[pattern]:
		return role != "hallendienst"
	case editorReads[pattern]:
		return role == "editor"
	case ownWrites[pattern]:
		return true
	case method == http.MethodGet:
		return true
	default:
		return role == "editor"
	}
}

func fillPattern(path string) string {
	return strings.NewReplacer("{id}", "999999", "{year}", "1999", "{month}", "1999-01", "{entity}", "persons",
		"{name}", "nicht-vorhanden.dump", "{handle}", "x").Replace(path)
}

func TestRolesAcrossAllRoutes(t *testing.T) {
	pool := testPool(t)
	mgr, err := auth.NewManager(pool, auth.SessionConfig{MaxAge: 3600}, false, false, "roles-integration-test-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	wa, err := auth.NewWebAuthnService(pool, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	router, _, err := New(pool, mgr, wa, 0, "", false, false, false,
		"", "", "", backup.S3Config{}, mail.New(mail.Config{}), "", stop, func(func()) {})
	if err != nil {
		t.Fatal(err)
	}
	patterns := append([]string(nil), routePatterns...)
	if len(patterns) < 200 {
		t.Fatalf("nur %d Routen erfasst — fehlt route(...) in server.go?", len(patterns))
	}
	roles := []string{"reader", "editor", "buchhaltung", "hallendienst"}
	cookies := map[string][]*http.Cookie{}
	for _, role := range roles {
		var id int64
		if err := pool.QueryRow(t.Context(),
			`INSERT INTO users (username, email, password_hash, role, is_admin) VALUES ($1, '', 'unused', $2, false) RETURNING id`,
			fmt.Sprintf("rolle-%s-%d", role, time.Now().UnixNano()), role).Scan(&id); err != nil {
			t.Fatalf("Benutzer %s: %v", role, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(t.Context(), `DELETE FROM users WHERE id = $1`, id) })
		rec := httptest.NewRecorder()
		if err := mgr.CreateSession(t.Context(), rec, httptest.NewRequest(http.MethodGet, "/", nil), id); err != nil {
			t.Fatal(err)
		}
		cookies[role] = rec.Result().Cookies()
	}
	var bad []string
	for _, pattern := range patterns {
		method, path, _ := strings.Cut(pattern, " ")
		// Eigene Anmeldung und Passkeys: jede Rolle, und ein Abmelden mitten im Lauf
		// würde die Sitzung für den Rest beenden.
		if strings.HasPrefix(path, "/api/auth/") || strings.HasPrefix(path, "/api/passkeys") {
			continue
		}
		for _, role := range roles {
			req := httptest.NewRequest(method, fillPattern(path), strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			for _, c := range cookies[role] {
				req.AddCookie(c)
				if c.Name == auth.CSRFCookie {
					req.Header.Set(auth.CSRFHeader, c.Value)
				}
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			body := rec.Body.String()
			denied := rec.Code == http.StatusForbidden &&
				(strings.Contains(body, "insufficient permissions") || strings.Contains(body, "admin privileges required"))
			if rec.Code == http.StatusUnauthorized {
				bad = append(bad, fmt.Sprintf("%-14s %s: 401 (Sitzung nicht angenommen?)", role, pattern))
				continue
			}
			if want := allowed(role, pattern); denied == want {
				verb := "abgewiesen"
				if !denied {
					verb = "durchgelassen"
				}
				bad = append(bad, fmt.Sprintf("%-14s %s: %s (Status %d)", role, pattern, verb, rec.Code))
			}
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("%d Abweichungen von der Rollenregel:\n  %s", len(bad), strings.Join(bad, "\n  "))
	}
}
