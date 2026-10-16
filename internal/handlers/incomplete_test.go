package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestPlanBlockContainsPoint(t *testing.T) {
	zone := planBlock{X: 0, Y: 0, W: 4, H: 2}
	if !zone.containsPoint(2, 1) || !zone.containsPoint(4, 2) || zone.containsPoint(4.01, 1) {
		t.Fatal("ungedrehte Zone: Mitte/Ecke drin, knapp daneben draußen")
	}
	// Dieselben Fälle wie tests/geometry/geometry.test.js: Planer und Server rechnen gleich.
	turned := planBlock{X: 0, Y: 0, W: 4, H: 2, Rot: 90}
	if !turned.containsPoint(2, 2.9) || turned.containsPoint(3.5, 1) {
		t.Fatal("um 90° gedrehte Zone stimmt nicht mit geometry.js überein")
	}
	if got := powerZones([]byte(`{"excl":[{"kind":"stell","x":0,"y":0,"w":5,"h":2},{"kind":"power","x":1,"y":1,"w":3,"h":2}]}`)); len(got) != 1 || got[0].X != 1 {
		t.Fatalf("powerZones = %+v, want only the power zone", got)
	}
	if powerZones([]byte(`kaputt`)) != nil || powerZones(nil) != nil {
		t.Fatal("unlesbare Geometrie heißt: keine Zonen")
	}
}

// Ein Gefährt mit Ladebedarf außerhalb der Stromzone wird gemeldet, eines darin nicht;
// in einer Halle ganz ohne Stromzone wird gar nicht gewarnt.
func TestIncompleteVehicles(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := createIntegrationPerson(t, h)
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	var catID, garageID, hallPower, hallPlain int64
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(h.Pool.QueryRow(ctx, `INSERT INTO categories (name, default_monthly_cost, default_yearly_cost) VALUES ($1, 20, 200) RETURNING id`, "Inc-"+tag).Scan(&catID))
	must(h.Pool.QueryRow(ctx, `INSERT INTO garages (name) VALUES ($1) RETURNING id`, "Inc-Garage-"+tag).Scan(&garageID))
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `UPDATE vehicles SET spot_id=NULL WHERE person_id=$1`, pid)
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM garages WHERE id=$1`, garageID)
	})
	must(h.Pool.QueryRow(ctx, `INSERT INTO halls (garage_id, name, geometry) VALUES ($1, 'Strom', $2) RETURNING id`, garageID,
		`{"excl":[{"kind":"power","x":0,"y":0,"w":6,"h":3,"rot":0}]}`).Scan(&hallPower))
	must(h.Pool.QueryRow(ctx, `INSERT INTO halls (garage_id, name, geometry) VALUES ($1, 'Ohne', '{"excl":[]}') RETURNING id`, garageID).Scan(&hallPlain))

	place := func(hall int64, label, geom string, needsPower bool, plate string, dims bool) int64 {
		t.Helper()
		var spot, veh int64
		must(h.Pool.QueryRow(ctx, `INSERT INTO spots (hall_id, label, geometry) VALUES ($1, $2, $3) RETURNING id`, hall, label+tag, geom).Scan(&spot))
		q := `INSERT INTO vehicles (person_id, category_id, label, license_plate, billing_period, rate, start_date, status, needs_power, spot_id, length_m, width_m)
		      VALUES ($1, $2, $3, $4, 'monthly', 20, '2026-01-01', 'stored', $5, $6, $7, $8) RETURNING id`
		var l, wd any
		if dims {
			l, wd = 4.5, 1.9
		}
		must(h.Pool.QueryRow(ctx, q, pid, catID, label, plate, needsPower, spot, l, wd).Scan(&veh))
		return veh
	}
	inside := place(hallPower, "drin", `{"x":1,"y":0.5,"w":4,"h":2}`, true, "IN-1", true)
	outside := place(hallPower, "draussen", `{"x":10,"y":5,"w":4,"h":2}`, true, "OUT-1", true)
	plain := place(hallPlain, "ohne-zonen", `{"x":10,"y":5,"w":4,"h":2}`, true, "", false)

	rec := httptest.NewRecorder()
	h.IncompleteVehicles(rec, httptest.NewRequest(http.MethodGet, "/api/vehicles/incomplete", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got incompleteVehicles
	must(json.Unmarshal(rec.Body.Bytes(), &got))
	found := map[int64]bool{}
	for _, pm := range got.PowerMissing {
		found[pm.VehicleID] = true
	}
	if !found[outside] {
		t.Errorf("Gefährt außerhalb der Stromzone fehlt: %+v", got.PowerMissing)
	}
	if found[inside] {
		t.Error("Gefährt in der Stromzone darf nicht gemeldet werden")
	}
	if found[plain] {
		t.Error("in einer Halle ohne Stromzonen wird nicht gewarnt")
	}
	// Die Zählung ist datenbankweit; das Gefährt ohne Maße und Kennzeichen zählt mit.
	if got.NoDimensions < 1 || got.NoPlate < 1 {
		t.Errorf("no_dimensions=%d no_plate=%d, want each >= 1", got.NoDimensions, got.NoPlate)
	}
}
