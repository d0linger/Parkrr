package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/models"
)

// --- helpers ---

func spUnique(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func jsonReq(method, path string, body []byte) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// idReq is a JSON request with the {id} path value populated (handlers are called
// directly here, bypassing the mux that would otherwise set it).
func idReq(method, path string, id int64, body []byte) *http.Request {
	r := jsonReq(method, path, body)
	r.SetPathValue("id", strconv.FormatInt(id, 10))
	return r
}

// mkGarage creates a garage via the handler and registers a cascade cleanup.
func mkGarage(t *testing.T, h *Handler) (int64, string) {
	t.Helper()
	name := spUnique("Garage")
	body, _ := json.Marshal(map[string]any{"name": name})
	rec := httptest.NewRecorder()
	h.CreateGarage(rec, jsonReq(http.MethodPost, "/api/garages", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create garage: %d %s", rec.Code, rec.Body.String())
	}
	var g models.Garage
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM garages WHERE id=$1`, g.ID) })
	return g.ID, name
}

func mkHall(t *testing.T, h *Handler, garageID int64, geometry any) models.Hall {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": spUnique("Halle"), "geometry": geometry})
	rec := httptest.NewRecorder()
	h.CreateHall(rec, idReq(http.MethodPost, "/api/garages/x/halls", garageID, body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create hall: %d %s", rec.Code, rec.Body.String())
	}
	var hl models.Hall
	_ = json.Unmarshal(rec.Body.Bytes(), &hl)
	return hl
}

func mkSpot(t *testing.T, h *Handler, hallID int64, label string, geometry any) models.Spot {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"label": label, "geometry": geometry})
	rec := httptest.NewRecorder()
	h.CreateSpot(rec, idReq(http.MethodPost, "/api/halls/x/spots", hallID, body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create spot %s: %d %s", label, rec.Code, rec.Body.String())
	}
	var s models.Spot
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	return s
}

type planResponse struct {
	Hall       models.Hall   `json:"hall"`
	GarageName string        `json:"garage_name"`
	Spots      []models.Spot `json:"spots"`
}

func getPlan(t *testing.T, h *Handler, hallID int64) (planResponse, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.GetHallPlan(rec, idReq(http.MethodGet, "/api/halls/x/plan", hallID, nil))
	var p planResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	return p, rec.Code
}

// --- tests ---

// TestGarageCRUD: create → list → rename → duplicate-name conflict → delete.
func TestGarageCRUD(t *testing.T) {
	h := testHandler(t)
	gid, name := mkGarage(t, h)

	// List includes it.
	lrec := httptest.NewRecorder()
	h.ListGarages(lrec, jsonReq(http.MethodGet, "/api/garages", nil))
	if lrec.Code != http.StatusOK || !strings.Contains(lrec.Body.String(), name) {
		t.Fatalf("list garages missing %q: %d %s", name, lrec.Code, lrec.Body.String())
	}

	// Rename.
	newName := spUnique("Garage-Renamed")
	urec := httptest.NewRecorder()
	ubody, _ := json.Marshal(map[string]any{"name": newName})
	h.UpdateGarage(urec, idReq(http.MethodPut, "/api/garages/x", gid, ubody))
	if urec.Code != http.StatusOK {
		t.Fatalf("rename garage: %d %s", urec.Code, urec.Body.String())
	}

	// A second garage may not reuse the (case-insensitive) name.
	crec := httptest.NewRecorder()
	cbody, _ := json.Marshal(map[string]any{"name": strings.ToUpper(newName)})
	h.CreateGarage(crec, jsonReq(http.MethodPost, "/api/garages", cbody))
	if crec.Code != http.StatusConflict {
		t.Fatalf("duplicate garage name should be 409, got %d %s", crec.Code, crec.Body.String())
	}

	// Delete.
	drec := httptest.NewRecorder()
	h.DeleteGarage(drec, idReq(http.MethodDelete, "/api/garages/x", gid, nil))
	if drec.Code != http.StatusOK {
		t.Fatalf("delete garage: %d %s", drec.Code, drec.Body.String())
	}
	drec2 := httptest.NewRecorder()
	h.DeleteGarage(drec2, idReq(http.MethodDelete, "/api/garages/x", gid, nil))
	if drec2.Code != http.StatusNotFound {
		t.Fatalf("deleting a gone garage should be 404, got %d", drec2.Code)
	}
}

// TestHallSpotCascade: geometry round-trips, spot_count is derived, duplicate hall
// names within a garage conflict, and deleting the garage cascades halls+spots.
func TestHallSpotCascade(t *testing.T) {
	h := testHandler(t)
	gid, _ := mkGarage(t, h)
	hall := mkHall(t, h, gid, map[string]any{"floor": [][]float64{{0, 0}, {10, 0}, {10, 10}, {0, 10}}})
	_ = mkSpot(t, h, hall.ID, "A1", map[string]any{"x": 1.0, "y": 1.0, "w": 2.5, "h": 5.0})

	// The plan returns the hall geometry and the one spot.
	plan, code := getPlan(t, h, hall.ID)
	if code != http.StatusOK {
		t.Fatalf("plan: %d", code)
	}
	if len(plan.Spots) != 1 || plan.Spots[0].Label != "A1" {
		t.Fatalf("plan should list spot A1; got %+v", plan.Spots)
	}
	if !strings.Contains(string(plan.Hall.Geometry), "floor") {
		t.Fatalf("hall geometry should round-trip; got %s", plan.Hall.Geometry)
	}

	// Duplicate hall name within the same garage → 409.
	dupBody, _ := json.Marshal(map[string]any{"name": strings.ToUpper(hall.Name), "geometry": map[string]any{}})
	drec := httptest.NewRecorder()
	h.CreateHall(drec, idReq(http.MethodPost, "/api/garages/x/halls", gid, dupBody))
	if drec.Code != http.StatusConflict {
		t.Fatalf("duplicate hall name should be 409, got %d %s", drec.Code, drec.Body.String())
	}

	// Deleting the garage cascades: the hall's plan is gone (404).
	drec2 := httptest.NewRecorder()
	h.DeleteGarage(drec2, idReq(http.MethodDelete, "/api/garages/x", gid, nil))
	if drec2.Code != http.StatusOK {
		t.Fatalf("delete garage: %d %s", drec2.Code, drec2.Body.String())
	}
	if _, code := getPlan(t, h, hall.ID); code != http.StatusNotFound {
		t.Fatalf("hall should be gone after garage delete, got %d", code)
	}
}

// TestSpotAssignment: the 1:1 link — assign, occupied-conflict, unassign frees,
// and deleting a spot frees its vehicle (SET NULL).
func TestSpotAssignment(t *testing.T) {
	h := testHandler(t)
	gid, _ := mkGarage(t, h)
	hall := mkHall(t, h, gid, map[string]any{})
	a1 := mkSpot(t, h, hall.ID, "A1", map[string]any{})
	a2 := mkSpot(t, h, hall.ID, "A2", map[string]any{})

	pid := createIntegrationPerson(t, h)
	v1 := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(1).Format("2006-01-02"))
	v2 := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(1).Format("2006-01-02"))

	assign := func(spotID, vehID int64) int {
		body, _ := json.Marshal(map[string]any{"vehicle_id": vehID})
		rec := httptest.NewRecorder()
		h.AssignSpotVehicle(rec, idReq(http.MethodPut, "/api/spots/x/vehicle", spotID, body))
		return rec.Code
	}

	if code := assign(a1.ID, v1); code != http.StatusOK {
		t.Fatalf("assign v1→A1: %d", code)
	}
	// Plan shows v1 on A1.
	plan, _ := getPlan(t, h, hall.ID)
	var a1Veh *int64
	for _, s := range plan.Spots {
		if s.ID == a1.ID {
			a1Veh = s.VehicleID
		}
	}
	if a1Veh == nil || *a1Veh != v1 {
		t.Fatalf("A1 should carry v1; got %v", a1Veh)
	}

	// v1 no longer appears among unassigned vehicles.
	urec := httptest.NewRecorder()
	h.ListUnassignedVehicles(urec, jsonReq(http.MethodGet, "/api/vehicles/unassigned", nil))
	if strings.Contains(urec.Body.String(), `"id":`+strconv.FormatInt(v1, 10)+`,`) {
		t.Fatalf("assigned v1 must not be in the unassigned list: %s", urec.Body.String())
	}

	// A second vehicle cannot take the occupied spot.
	if code := assign(a1.ID, v2); code != http.StatusConflict {
		t.Fatalf("assigning to an occupied spot should be 409, got %d", code)
	}
	// But it can take a free one.
	if code := assign(a2.ID, v2); code != http.StatusOK {
		t.Fatalf("assign v2→A2: %d", code)
	}

	// Unassign A1 frees v1.
	frec := httptest.NewRecorder()
	h.UnassignSpotVehicle(frec, idReq(http.MethodDelete, "/api/spots/x/vehicle", a1.ID, nil))
	if frec.Code != http.StatusOK {
		t.Fatalf("unassign A1: %d %s", frec.Code, frec.Body.String())
	}
	plan, _ = getPlan(t, h, hall.ID)
	for _, s := range plan.Spots {
		if s.ID == a1.ID && s.VehicleID != nil {
			t.Fatalf("A1 should be free after unassign; got vehicle %d", *s.VehicleID)
		}
	}

	// Deleting A2 frees v2 (SET NULL), which then reappears as unassigned.
	drec := httptest.NewRecorder()
	h.DeleteSpot(drec, idReq(http.MethodDelete, "/api/spots/x", a2.ID, nil))
	if drec.Code != http.StatusOK {
		t.Fatalf("delete A2: %d %s", drec.Code, drec.Body.String())
	}
	urec2 := httptest.NewRecorder()
	h.ListUnassignedVehicles(urec2, jsonReq(http.MethodGet, "/api/vehicles/unassigned", nil))
	if !strings.Contains(urec2.Body.String(), `"id":`+strconv.FormatInt(v2, 10)+`,`) {
		t.Fatalf("v2 should be unassigned again after its spot was deleted: %s", urec2.Body.String())
	}
}

// TestVehicleDimensions: setting dimensions round-trips into the unassigned
// palette and the hall plan; an out-of-range value is a clean 400.
func TestVehicleDimensions(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(1).Format("2006-01-02"))

	// Set valid dimensions.
	body, _ := json.Marshal(map[string]any{"length_m": 4.6, "width_m": 1.8, "height_m": 1.5, "weight_t": 1.4})
	rec := httptest.NewRecorder()
	h.SetVehicleDimensions(rec, idReq(http.MethodPut, "/api/vehicles/x/dimensions", vid, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("set dims: %d %s", rec.Code, rec.Body.String())
	}
	// They surface in the unassigned palette.
	urec := httptest.NewRecorder()
	h.ListUnassignedVehicles(urec, jsonReq(http.MethodGet, "/api/vehicles/unassigned", nil))
	if !strings.Contains(urec.Body.String(), `"height_m":1.5`) {
		t.Fatalf("dimensions should appear in the palette: %s", urec.Body.String())
	}
	// And in the hall plan once placed.
	gid, _ := mkGarage(t, h)
	hall := mkHall(t, h, gid, map[string]any{})
	spot := mkSpot(t, h, hall.ID, "P1", map[string]any{"x": 0, "y": 0, "w": 4.6, "h": 1.8})
	abody, _ := json.Marshal(map[string]any{"vehicle_id": vid})
	arec := httptest.NewRecorder()
	h.AssignSpotVehicle(arec, idReq(http.MethodPut, "/api/spots/x/vehicle", spot.ID, abody))
	if arec.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", arec.Code, arec.Body.String())
	}
	plan, _ := getPlan(t, h, hall.ID)
	var ok bool
	for _, s := range plan.Spots {
		if s.VehicleID != nil && *s.VehicleID == vid && s.HeightM != nil && *s.HeightM == 1.5 {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("placed vehicle should carry its height in the plan: %+v", plan.Spots)
	}
	// Out-of-range height → 400.
	bad, _ := json.Marshal(map[string]any{"height_m": 99})
	brec := httptest.NewRecorder()
	h.SetVehicleDimensions(brec, idReq(http.MethodPut, "/api/vehicles/x/dimensions", vid, bad))
	if brec.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range height should be 400, got %d", brec.Code)
	}
}

// TestSpotGeometryTooLarge: an over-cap geometry blob is rejected (400), guarding
// DB bloat even though the request body itself is under the 1 MiB decode limit.
func TestSpotGeometryTooLarge(t *testing.T) {
	h := testHandler(t)
	gid, _ := mkGarage(t, h)
	hall := mkHall(t, h, gid, map[string]any{})

	big := strings.Repeat("a", maxGeometryLen+10)
	body, _ := json.Marshal(map[string]any{"label": "X1", "geometry": map[string]any{"blob": big}})
	rec := httptest.NewRecorder()
	h.CreateSpot(rec, idReq(http.MethodPost, "/api/halls/x/spots", hall.ID, body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized geometry should be 400, got %d %s", rec.Code, rec.Body.String())
	}
}
