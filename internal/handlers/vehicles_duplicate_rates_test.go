package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/models"
)

func TestDuplicateVehicleKeepsEffectiveAndScheduledRates(t *testing.T) {
	for _, tc := range []struct {
		name, start string
		current     bool
		freeChange  bool
		rate        float64
		future      int
	}{
		{name: "before first change", start: "2026-09-01", rate: 60, future: 3},
		{name: "at change boundary", start: "2026-10-01", rate: 65, future: 2},
		{name: "between changes", start: "2026-11-01", rate: 65, future: 2},
		{name: "effective zero rate remains free", start: "2026-11-01", freeChange: true, future: 2},
		{name: "after all changes", start: "2027-02-01", rate: 90},
		{name: "new current tariff replaces agreed schedule", start: "2026-11-01", current: true, rate: 70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler(t)
			_, source, category := seasonFixture(t, h, "2026-03-31")
			if _, err := h.Pool.Exec(t.Context(), `INSERT INTO vehicle_rate_changes
				(vehicle_id,effective_from,rate,previous_rate,reason) VALUES
				($1,'2026-10-01',65,60,'first'),($1,'2026-12-01',80,65,'second'),($1,'2027-01-01',90,80,'third')`, source); err != nil {
				t.Fatal(err)
			}
			if tc.freeChange {
				if _, err := h.Pool.Exec(t.Context(), `UPDATE vehicle_rate_changes SET rate=0 WHERE vehicle_id=$1 AND effective_from='2026-10-01'`, source); err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(source, 10)+"/duplicate",
				strings.NewReader(fmt.Sprintf(`{"start_date":%q,"current_tariff":%t}`, tc.start, tc.current)))
			req.SetPathValue("id", strconv.FormatInt(source, 10))
			w := httptest.NewRecorder()
			h.DuplicateVehicle(w, req)
			if w.Code != http.StatusCreated {
				t.Fatalf("duplicate: %d %s", w.Code, w.Body.String())
			}
			var copied models.Vehicle
			if err := json.Unmarshal(w.Body.Bytes(), &copied); err != nil {
				t.Fatal(err)
			}
			if copied.Rate != tc.rate || len(copied.RateChanges) != tc.future {
				t.Fatalf("copy rate=%v schedule=%v, want rate=%v and %d future changes", copied.Rate, copied.RateChanges, tc.rate, tc.future)
			}
			p := copied.AsPeriod(models.Category{ID: category, DefaultMonthlyCost: 70})
			if got := p.RateAt(copied.StartDate); got != tc.rate {
				t.Errorf("effective copied rate=%v, want %v", got, tc.rate)
			}
			for _, rc := range copied.RateChanges {
				if !rc.From.After(copied.StartDate) {
					t.Errorf("copied an already-effective rate change: %+v", rc)
				}
				if got := p.RateAt(rc.From.AddDate(0, 0, -1)); got >= rc.Amount {
					t.Errorf("scheduled rate %.2f applied before %s: %.2f", rc.Amount, rc.From.Format(time.DateOnly), got)
				}
				if got := p.RateAt(rc.From); got != rc.Amount {
					t.Errorf("rate at %s=%v, want %v", rc.From.Format(time.DateOnly), got, rc.Amount)
				}
			}
			var sourceChanges int
			if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM vehicle_rate_changes WHERE vehicle_id=$1`, source).Scan(&sourceChanges); err != nil || sourceChanges != 3 {
				t.Errorf("source schedule changed: count=%d err=%v", sourceChanges, err)
			}
		})
	}
}
