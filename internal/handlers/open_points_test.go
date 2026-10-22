package handlers

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// Ausbauplan, offene Punkte vom 22.10.2026: O1 Flächenauslastung, O3 Vorjahresplatz,
// O6 „Ist zurück“ durch den Hallendienst.

func testGarage(t *testing.T, h *Handler) (garage int64, tag string) {
	t.Helper()
	tag = strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.Pool.QueryRow(context.Background(), `INSERT INTO garages (name) VALUES ('Offen-'||$1) RETURNING id`, tag).Scan(&garage); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM garages WHERE id = $1`, garage) })
	return garage, tag
}

func testHall(t *testing.T, h *Handler, garage int64, name, geometry string) int64 {
	t.Helper()
	var id int64
	if err := h.Pool.QueryRow(context.Background(), `INSERT INTO halls (garage_id, name, geometry) VALUES ($1, $2, $3::jsonb) RETURNING id`, garage, name, geometry).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func testSpot(t *testing.T, h *Handler, hall int64, label, geometry string) int64 {
	t.Helper()
	var id int64
	if err := h.Pool.QueryRow(context.Background(), `INSERT INTO spots (hall_id, label, geometry) VALUES ($1, $2, $3::jsonb) RETURNING id`, hall, label, geometry).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// catFoot muss dieselben Standardmaße liefern wie catFoot in app.js.
func TestCatFootMatchesPlanner(t *testing.T) {
	for typ, want := range map[string][2]float64{
		"": {4.5, 1.9}, "PKW": {4.5, 1.9}, "Wohnmobil": {7.0, 2.3}, "Reisemobil groß": {7.0, 2.3}, "Motorrad": {2.2, 0.9},
		"Wohnwagen": {6.0, 2.2}, "Anhänger": {4.0, 1.8}, "Pferdeanhänger": {4.0, 1.8}, "Bootstrailer": {6.0, 2.3},
		"Traktor": {4.6, 2.2}, "Ladewagen": {8.0, 2.5}, "Rückewagen": {7.5, 2.4}, "Kipper": {6.5, 2.4},
		"Kastenwagen": {5.4, 2.1}, "VW Bus": {5.4, 2.1}, "Minivan groß": {4.5, 1.9}, "Mini Van": {5.4, 2.1},
	} {
		if got := catFoot(typ); got != want {
			t.Errorf("catFoot(%q) = %v, want %v", typ, got, want)
		}
	}
	// num() aus app.js: Zahl gilt, null zählt als 0, fehlend oder Text nimmt den Ersatz.
	l, w := 6.0, 2.4
	for _, c := range []struct {
		gw, gh string
		want   float64
	}{{"4", "2", 8}, {"", "", 14.4}, {"null", "2", 0}, {`"x"`, "2", 12}} {
		var gw, gh []byte
		if c.gw != "" {
			gw = []byte(c.gw)
		}
		if c.gh != "" {
			gh = []byte(c.gh)
		}
		if got := spotFootprint(gw, gh, &l, &w, "Wohnmobil"); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("spotFootprint(%s, %s) = %v, want %v", c.gw, c.gh, got, c.want)
		}
	}
}

// O1: Auslastung = belegte m² aller Plätze / nutzbare Fläche aus dem Planer.
func TestOccupancyAreaPercent(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	_, vid, _ := seasonFixture(t, h, "")
	garage, _ := testGarage(t, h)
	full := testHall(t, h, garage, "Fläche", `{"floor":[[0,0],[10,0],[10,8],[0,8]],"park":50}`)
	open := testHall(t, h, garage, "Ohne Fläche", `{"floor":[[0,0],[6,0],[6,6],[0,6]]}`)
	s1 := testSpot(t, h, full, "A-01", `{"x":1,"y":1,"w":4,"h":2,"rot":0}`)
	testSpot(t, h, full, "A-02", `{}`)                           // leer, ohne Maße: PKW 4,5 × 1,9
	testSpot(t, h, full, "A-03", `{"x":1,"y":5,"w":3,"h":null}`) // null zählt wie im Planer als 0
	testSpot(t, h, open, "B-01", `{"x":1,"y":1,"w":2,"h":2}`)
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET spot_id = $2 WHERE id = $1`, vid, s1); err != nil {
		t.Fatal(err)
	}
	read := func() map[int64]hallOccupancy {
		t.Helper()
		w := callJSON(h, h.Occupancy, http.MethodGet, "/api/occupancy", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("occupancy: %d %s", w.Code, w.Body.String())
		}
		var resp occupancyResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		out := map[int64]hallOccupancy{}
		for _, ho := range resp.Halls {
			out[ho.HallID] = ho
		}
		return out
	}
	got := read()
	if f := got[full]; f.UsedM2 != 16.55 || f.Pct == nil || *f.Pct != 33 || f.Placed != 1 {
		t.Fatalf("Halle mit Fläche: %+v (pct %v), want 16,55 m², 33 %%, 1 platziert", f, f.Pct)
	}
	if o := got[open]; o.UsedM2 != 4 || o.Pct != nil || o.ParkM2 != nil {
		t.Fatalf("Halle ohne Fläche: %+v, want 4 m² und keine Prozentzahl", o)
	}

	// Der Planer meldet die Fläche nach; nur geometry.park ändert sich.
	set := func(id int64, body string) *httptest.ResponseRecorder {
		return callJSONRaw(h, h.SetHallParkArea, http.MethodPut, "/api/halls/x/park-area", strconv.FormatInt(id, 10), body)
	}
	if w := set(open, `{"park_m2":20}`); w.Code != http.StatusOK {
		t.Fatalf("park-area: %d %s", w.Code, w.Body.String())
	}
	if o := read()[open]; o.Pct == nil || *o.Pct != 20 {
		t.Fatalf("nach dem Nachtragen: %+v", o)
	}
	var floorLen int
	if err := h.Pool.QueryRow(ctx, `SELECT jsonb_array_length(geometry->'floor') FROM halls WHERE id = $1`, open).Scan(&floorLen); err != nil || floorLen != 4 {
		t.Fatalf("der übrige Grundriss bleibt: %d %v", floorLen, err)
	}
	var audits int
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE entity = 'hall' AND entity_id = $1 AND summary LIKE 'Parkfläche aus dem Planer%'`, open).Scan(&audits)
	if audits != 1 {
		t.Fatalf("Protokoll: %d Einträge, want 1", audits)
	}
	if w := set(open, `{"park_m2":20.001}`); w.Code != http.StatusOK {
		t.Fatalf("gleicher Wert: %d", w.Code)
	}
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE entity = 'hall' AND entity_id = $1 AND summary LIKE 'Parkfläche aus dem Planer%'`, open).Scan(&audits)
	if audits != 1 {
		t.Fatalf("unveränderte Fläche schreibt kein zweites Protokoll: %d", audits)
	}
	for body, want := range map[string]int{`{"park_m2":-1}`: 400, `{}`: 400, `{"park_m2":"x"}`: 400, `{"park_m2":2000000}`: 400} {
		if w := set(open, body); w.Code != want {
			t.Errorf("%s: %d, want %d", body, w.Code, want)
		}
	}
	if w := set(open+1000000, `{"park_m2":10}`); w.Code != http.StatusNotFound {
		t.Errorf("unbekannte Halle: %d", w.Code)
	}
}

// O3: Die Kopie aus „Erneut einstellen“ kennt ihren Vorgänger; die Ablageleiste nennt
// dessen letzten Platz, solange es ihn gibt.
func TestPaletteNamesPreviousSeasonSpot(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	_, vid, _ := seasonFixture(t, h, "")
	garage, _ := testGarage(t, h)
	hall := testHall(t, h, garage, "Nord", `{}`)
	spot := testSpot(t, h, hall, "B-07", `{"x":1,"y":1,"w":7,"h":2.3}`)
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET spot_id = $2 WHERE id = $1`, vid, spot); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET status = 'collected', end_date = '2026-03-31', spot_id = NULL WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	w := callJSONRaw(h, h.DuplicateVehicle, http.MethodPost, "/api/vehicles/x/duplicate", strconv.FormatInt(vid, 10), `{"start_date":"2026-11-01"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	var from *int64
	if err := h.Pool.QueryRow(ctx, `SELECT copied_from FROM vehicles WHERE id = $1`, created.ID).Scan(&from); err != nil || from == nil || *from != vid {
		t.Fatalf("copied_from: %v %v, want %d", from, err, vid)
	}
	palette := func() *unassignedVehicle {
		t.Helper()
		w := callJSON(h, h.ListUnassignedVehicles, http.MethodGet, "/api/vehicles/unassigned", "", nil)
		var out []unassignedVehicle
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		for i := range out {
			if out[i].ID == created.ID {
				return &out[i]
			}
		}
		t.Fatalf("Kopie fehlt in der Ablageleiste: %s", w.Body.String())
		return nil
	}
	p := palette()
	if p.PrevSpotID == nil || *p.PrevSpotID != spot || p.PrevHallID == nil || *p.PrevHallID != hall || !p.PrevFromCopy || p.PrevSpotLabel == nil || *p.PrevSpotLabel != "B-07" {
		t.Fatalf("Vorjahresplatz: %+v", p)
	}
	if _, err := h.Pool.Exec(ctx, `DELETE FROM spots WHERE id = $1`, spot); err != nil {
		t.Fatal(err)
	}
	if p := palette(); p.PrevSpotID != nil {
		t.Fatalf("gelöschter Platz wird nicht mehr vorgeschlagen: %+v", p)
	}
}

// Migration 088 trägt den Vorgänger früherer Kopien aus dem Änderungsprotokoll nach.
func TestMigrationBackfillsCopiedFrom(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	_, src, _ := seasonFixture(t, h, "2026-03-31")
	_, cp, _ := seasonFixture(t, h, "")
	if _, err := h.Pool.Exec(ctx, `INSERT INTO audit_log (username, action, entity, entity_id, summary) VALUES ('t', 'create', 'vehicle', $1, $2)`,
		cp, "re-stored vehicle from #"+strconv.FormatInt(src, 10)); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile(filepath.Join("..", "database", "migrations", "088_vehicle_copied_from.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("Migration erneut ausführen: %v", err)
	}
	var from *int64
	if err := h.Pool.QueryRow(ctx, `SELECT copied_from FROM vehicles WHERE id = $1`, cp).Scan(&from); err != nil || from == nil || *from != src {
		t.Fatalf("nachgetragen: %v %v, want %d", from, err, src)
	}
}

// O6: Der Hallendienst tippt „Ist zurück“ — bis heute, ohne „bezahlt bis“ zu sehen.
func TestHallStaffReturnsFromPause(t *testing.T) {
	h := testHandler(t)
	_, vid := pauseFixture(t, h)
	if _, err := h.Pool.Exec(context.Background(), `UPDATE vehicles SET paid = true, paid_through = '2026-07-31' WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	h.Now = func() time.Time { return time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC) }
	p, code, msg := startPause(t, h, vid, `{"from":"2026-07-10"}`)
	if p == nil {
		t.Fatalf("pause: %d %s", code, msg)
	}
	h.Now = func() time.Time { return time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC) }
	hall := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/vehicle-pauses/x/return", strings.NewReader(body))
		r.SetPathValue("id", strconv.FormatInt(p.ID, 10))
		r = r.WithContext(auth.ContextWithUser(r.Context(), &models.User{ID: 1, Username: "halle", Role: models.RoleHall}))
		w := httptest.NewRecorder()
		h.ReturnFromPause(w, r)
		return w
	}
	if w := hall(`{"on":"2026-07-25"}`); w.Code != http.StatusForbidden {
		t.Fatalf("Rückkehr in der Zukunft durch den Hallendienst: %d %s", w.Code, w.Body.String())
	}
	w := hall(`{}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"paid_through":null`) {
		t.Fatalf("Ist zurück durch den Hallendienst: %d %s", w.Code, w.Body.String())
	}
	var through time.Time
	if err := h.Pool.QueryRow(context.Background(), `SELECT paid_through FROM vehicles WHERE id = $1`, vid).Scan(&through); err != nil || through.Format(dateLayout) != "2026-08-10" {
		t.Fatalf("„bezahlt bis“ verschiebt sich trotzdem: %s %v", through.Format(dateLayout), err)
	}
}
