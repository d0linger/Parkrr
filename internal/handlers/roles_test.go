package handlers

import (
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
