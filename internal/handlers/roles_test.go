package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// Ausbau 35: Gefährte-, Tarif-, Dienst- und Servicelisten liest auch der Hallendienst
// (Planer, Übergaben) — aber ohne Beträge. Die Buchhaltung sieht sie unverändert.
func TestHallStaffSeesNoAmounts(t *testing.T) {
	h := testHandler(t)
	pid, _, catID := seasonFixture(t, h, "")
	as := func(role string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/vehicles?person_id="+strconv.FormatInt(pid, 10), nil)
		return r.WithContext(auth.ContextWithUser(r.Context(), &models.User{ID: 1, Role: role}))
	}
	list := func(role string) []models.Vehicle {
		w := httptest.NewRecorder()
		h.ListVehicles(w, as(role))
		var out []models.Vehicle
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if len(out) != 1 {
			t.Fatalf("%s: %d Gefährte (%s)", role, len(out), w.Body.String())
		}
		return out
	}
	if v := list(models.RoleAccounting)[0]; v.Rate != 60 || v.AccruedCost <= 0 {
		t.Fatalf("Buchhaltung sieht die Beträge: rate %v accrued %v", v.Rate, v.AccruedCost)
	}
	if v := list(models.RoleHall)[0]; v.Rate != 0 || v.AccruedCost != 0 || v.EffectiveRate != 0 || v.Label == "" {
		t.Fatalf("Hallendienst: Beträge sichtbar oder Gefährt leer: %+v", v)
	}
	w := httptest.NewRecorder()
	h.ListCategories(w, as(models.RoleHall))
	if strings.Contains(w.Body.String(), `"id":`+strconv.FormatInt(catID, 10)+`,"name"`) && !strings.Contains(w.Body.String(), `"default_monthly_cost":0`) {
		t.Fatalf("Tarifpreise für den Hallendienst: %s", w.Body.String())
	}
	var cats []struct {
		ID      int64   `json:"id"`
		Monthly float64 `json:"default_monthly_cost"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &cats)
	for _, c := range cats {
		if c.Monthly != 0 {
			t.Fatalf("Tarif %d mit Preis %v für den Hallendienst", c.ID, c.Monthly)
		}
	}
}

func TestHallStaffCannotDownloadAcceptedContract(t *testing.T) {
	h := testHandler(t)
	pid, vid, _ := seasonFixture(t, h, "")
	var templateID, contractID, ordinaryID int64
	if err := h.Pool.QueryRow(t.Context(), `INSERT INTO contract_templates (version, title, body)
		SELECT COALESCE(max(version), 0) + 1, 'Vertrag', 'Preis: {{preis}}' FROM contract_templates RETURNING id`).Scan(&templateID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM contract_acceptances WHERE template_id = $1`, templateID)
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM contract_templates WHERE id = $1`, templateID)
	})
	for _, a := range []struct {
		filename string
		id       *int64
	}{{"Einstellvertrag.pdf", &contractID}, {"Zustand.pdf", &ordinaryID}} {
		if err := h.Pool.QueryRow(t.Context(), `INSERT INTO attachments (person_id, owner_person_id, filename, content_type, byte_size, data)
			VALUES ($1, $1, $2, 'application/pdf', 5, $3) RETURNING id`, pid, a.filename, []byte("%PDF-")).Scan(a.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO contract_acceptances (person_id, vehicle_id, template_id, via, attachment_id)
		VALUES ($1, $2, $3, 'portal', $4)`, pid, vid, templateID, contractID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		role string
		want int
	}{{models.RoleHall, http.StatusNotFound}, {models.RoleAccounting, http.StatusOK}} {
		t.Run(tc.role, func(t *testing.T) {
			request := func(path string, id int64) *http.Request {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r.SetPathValue("id", strconv.FormatInt(id, 10))
				return r.WithContext(auth.ContextWithUser(r.Context(), &models.User{Role: tc.role}))
			}
			w := httptest.NewRecorder()
			h.GetAttachment(w, request("/api/attachments/x", contractID))
			if w.Code != tc.want {
				t.Errorf("contract download = %d, want %d", w.Code, tc.want)
			}
			w = httptest.NewRecorder()
			h.GetAttachment(w, request("/api/attachments/x", ordinaryID))
			if w.Code != http.StatusOK {
				t.Errorf("ordinary attachment download = %d, want 200", w.Code)
			}
			w = httptest.NewRecorder()
			h.ListAttachments(w, request("/api/persons/x/attachments", pid))
			if w.Code != http.StatusOK {
				t.Fatalf("attachment list = %d: %s", w.Code, w.Body.String())
			}
			var attachments []attachmentMeta
			if err := json.Unmarshal(w.Body.Bytes(), &attachments); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, a := range attachments {
				found = found || a.ID == contractID
			}
			if found != (tc.want == http.StatusOK) {
				t.Errorf("contract listed = %v for %s", found, tc.role)
			}
		})
	}
}
