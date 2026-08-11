package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// Stellplatz-/Hallen-Verwaltung (Garagenplaner) — a purely organizational spatial
// layer (garage → hall → spot) recording WHERE a Gefährt stands. It never touches
// pricing or invoicing. Geometry is opaque JSON the frontend planner owns; the
// backend validates only that it is well-formed and size-capped, then stores and
// returns it verbatim.

// normalizeGeometry validates the opaque planner geometry and returns the bytes to
// store. Empty/absent/null geometry becomes an empty object so the column stays
// non-null. Reports false when the blob is malformed or over the size cap.
func normalizeGeometry(raw json.RawMessage) (json.RawMessage, bool) {
	s := bytes.TrimSpace(raw)
	if len(s) == 0 || string(s) == "null" {
		return json.RawMessage(`{}`), true
	}
	if len(s) > maxGeometryLen || !json.Valid(s) {
		return nil, false
	}
	return s, true
}

// --- Garages ---

// ListGarages returns all garages with a derived hall count, newest sort first.
func (h *Handler) ListGarages(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT g.id, g.name, g.sort_order, g.created_at, g.updated_at,
		        (SELECT count(*) FROM halls WHERE garage_id = g.id)
		   FROM garages g
		  ORDER BY g.sort_order, lower(g.name)`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []models.Garage{}
	for rows.Next() {
		var g models.Garage
		if err := rows.Scan(&g.ID, &g.Name, &g.SortOrder, &g.CreatedAt, &g.UpdatedAt, &g.HallCount); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, g)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type garageRequest struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

// CreateGarage adds a garage (editor).
func (h *Handler) CreateGarage(w http.ResponseWriter, r *http.Request) {
	var req garageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = trim(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validNameLength(req.Name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	var g models.Garage
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO garages (name, sort_order) VALUES ($1,$2)
		 RETURNING id, name, sort_order, created_at, updated_at`,
		req.Name, req.SortOrder,
	).Scan(&g.ID, &g.Name, &g.SortOrder, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a garage with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create garage")
		return
	}
	h.audit(r, "create", "garage", g.ID, "created garage "+g.Name)
	writeJSON(w, http.StatusCreated, g)
}

// UpdateGarage renames/reorders a garage (editor).
func (h *Handler) UpdateGarage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req garageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = trim(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validNameLength(req.Name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE garages SET name=$1, sort_order=$2, updated_at=now() WHERE id=$3`,
		req.Name, req.SortOrder, id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a garage with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update garage")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "garage not found")
		return
	}
	h.audit(r, "update", "garage", id, "updated garage "+req.Name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteGarage removes a garage and (via ON DELETE CASCADE) its halls and spots;
// any vehicles on those spots are freed (ON DELETE SET NULL on vehicles.spot_id).
func (h *Handler) DeleteGarage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM garages WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete garage")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "garage not found")
		return
	}
	h.audit(r, "delete", "garage", id, "deleted garage")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Halls ---

// ListHalls returns the halls of a garage with a derived spot count.
func (h *Handler) ListHalls(w http.ResponseWriter, r *http.Request) {
	garageID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT h.id, h.garage_id, h.name, h.geometry, h.sort_order, h.created_at, h.updated_at,
		        (SELECT count(*) FROM spots WHERE hall_id = h.id)
		   FROM halls h
		  WHERE h.garage_id = $1
		  ORDER BY h.sort_order, lower(h.name)`, garageID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []models.Hall{}
	for rows.Next() {
		var hl models.Hall
		if err := rows.Scan(&hl.ID, &hl.GarageID, &hl.Name, &hl.Geometry, &hl.SortOrder,
			&hl.CreatedAt, &hl.UpdatedAt, &hl.SpotCount); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, hl)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type hallRequest struct {
	Name      string          `json:"name"`
	Geometry  json.RawMessage `json:"geometry"`
	SortOrder int             `json:"sort_order"`
}

// CreateHall adds a hall to a garage (editor).
func (h *Handler) CreateHall(w http.ResponseWriter, r *http.Request) {
	garageID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req hallRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = trim(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validNameLength(req.Name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	geom, ok := normalizeGeometry(req.Geometry)
	if !ok {
		writeError(w, http.StatusBadRequest, "geometry is invalid or too large")
		return
	}
	var hl models.Hall
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO halls (garage_id, name, geometry, sort_order) VALUES ($1,$2,$3,$4)
		 RETURNING id, garage_id, name, geometry, sort_order, created_at, updated_at`,
		garageID, req.Name, geom, req.SortOrder,
	).Scan(&hl.ID, &hl.GarageID, &hl.Name, &hl.Geometry, &hl.SortOrder, &hl.CreatedAt, &hl.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a hall with that name already exists in this garage")
			return
		}
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusNotFound, "garage not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create hall")
		return
	}
	h.audit(r, "create", "hall", hl.ID, "created hall "+hl.Name)
	writeJSON(w, http.StatusCreated, hl)
}

// UpdateHall edits a hall's name, floor geometry and order (editor).
func (h *Handler) UpdateHall(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req hallRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = trim(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validNameLength(req.Name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	geom, ok := normalizeGeometry(req.Geometry)
	if !ok {
		writeError(w, http.StatusBadRequest, "geometry is invalid or too large")
		return
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE halls SET name=$1, geometry=$2, sort_order=$3, updated_at=now() WHERE id=$4`,
		req.Name, geom, req.SortOrder, id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a hall with that name already exists in this garage")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update hall")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "hall not found")
		return
	}
	h.audit(r, "update", "hall", id, "updated hall "+req.Name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteHall removes a hall and (CASCADE) its spots; vehicles on those spots are
// freed (SET NULL).
func (h *Handler) DeleteHall(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM halls WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete hall")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "hall not found")
		return
	}
	h.audit(r, "delete", "hall", id, "deleted hall")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Spots ---

// spotsOfHall loads a hall's spots with the Gefährt (if any) currently on each.
func (h *Handler) spotsOfHall(r *http.Request, hallID int64) ([]models.Spot, error) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT s.id, s.hall_id, s.label, s.geometry, s.created_at, s.updated_at,
		        v.id,
		        COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), cat.name, 'Gefährt'),
		        COALESCE(cat.name, ''),
		        v.person_id, trim(p.first_name || ' ' || p.last_name),
		        v.length_m, v.width_m, v.height_m, v.weight_t, COALESCE(v.needs_power, false)
		   FROM spots s
		   LEFT JOIN vehicles   v   ON v.spot_id = s.id
		   LEFT JOIN categories cat ON cat.id = v.category_id
		   LEFT JOIN persons    p   ON p.id = v.person_id
		  WHERE s.hall_id = $1
		  ORDER BY lower(s.label)`, hallID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Spot{}
	for rows.Next() {
		var s models.Spot
		var vehID, personID *int64
		var vehLabel, vehType, personName *string
		if err := rows.Scan(&s.ID, &s.HallID, &s.Label, &s.Geometry, &s.CreatedAt, &s.UpdatedAt,
			&vehID, &vehLabel, &vehType, &personID, &personName,
			&s.LengthM, &s.WidthM, &s.HeightM, &s.WeightT, &s.NeedsPower); err != nil {
			return nil, err
		}
		if vehID != nil {
			s.VehicleID = vehID
			s.VehicleLabel = derefStr(vehLabel, "")
			s.VehicleType = derefStr(vehType, "")
			s.PersonID = personID
			s.PersonName = derefStr(personName, "")
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetHallPlan returns a hall (with floor geometry) and its spots (with vehicle
// assignment) in one shot — everything the planner needs to render.
func (h *Handler) GetHallPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var hl models.Hall
	var garageName string
	err := h.Pool.QueryRow(r.Context(),
		`SELECT h.id, h.garage_id, h.name, h.geometry, h.sort_order, h.created_at, h.updated_at, g.name
		   FROM halls h JOIN garages g ON g.id = h.garage_id
		  WHERE h.id=$1`, id,
	).Scan(&hl.ID, &hl.GarageID, &hl.Name, &hl.Geometry, &hl.SortOrder, &hl.CreatedAt, &hl.UpdatedAt, &garageName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "hall not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	spots, err := h.spotsOfHall(r, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hall":        hl,
		"garage_name": garageName,
		"spots":       spots,
	})
}

type spotRequest struct {
	Label    string          `json:"label"`
	Geometry json.RawMessage `json:"geometry"`
}

// CreateSpot adds a Stellplatz to a hall (editor).
func (h *Handler) CreateSpot(w http.ResponseWriter, r *http.Request) {
	hallID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req spotRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Label = trim(req.Label)
	if req.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}
	if !validNameLength(req.Label) {
		writeError(w, http.StatusBadRequest, "label is too long")
		return
	}
	geom, ok := normalizeGeometry(req.Geometry)
	if !ok {
		writeError(w, http.StatusBadRequest, "geometry is invalid or too large")
		return
	}
	var s models.Spot
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO spots (hall_id, label, geometry) VALUES ($1,$2,$3)
		 RETURNING id, hall_id, label, geometry, created_at, updated_at`,
		hallID, req.Label, geom,
	).Scan(&s.ID, &s.HallID, &s.Label, &s.Geometry, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a spot with that label already exists in this hall")
			return
		}
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusNotFound, "hall not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create spot")
		return
	}
	h.audit(r, "create", "spot", s.ID, "created spot "+s.Label)
	writeJSON(w, http.StatusCreated, s)
}

// UpdateSpot edits a spot's label and geometry (editor).
func (h *Handler) UpdateSpot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req spotRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Label = trim(req.Label)
	if req.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}
	if !validNameLength(req.Label) {
		writeError(w, http.StatusBadRequest, "label is too long")
		return
	}
	geom, ok := normalizeGeometry(req.Geometry)
	if !ok {
		writeError(w, http.StatusBadRequest, "geometry is invalid or too large")
		return
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE spots SET label=$1, geometry=$2, updated_at=now() WHERE id=$3`,
		req.Label, geom, id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a spot with that label already exists in this hall")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update spot")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "spot not found")
		return
	}
	h.audit(r, "update", "spot", id, "updated spot "+req.Label)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteSpot removes a spot; a vehicle standing on it is freed (SET NULL).
func (h *Handler) DeleteSpot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM spots WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete spot")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "spot not found")
		return
	}
	h.audit(r, "delete", "spot", id, "deleted spot")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Assignment (1:1, optional) ---

type assignRequest struct {
	VehicleID int64 `json:"vehicle_id"`
}

// AssignSpotVehicle places a Gefährt on a spot (editor). The link is 1:1: the
// partial unique index rejects a second Gefährt on the same spot (409). Moving a
// Gefährt that already stands elsewhere simply frees its previous spot.
func (h *Handler) AssignSpotVehicle(w http.ResponseWriter, r *http.Request) {
	spotID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req assignRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.VehicleID <= 0 {
		writeError(w, http.StatusBadRequest, "vehicle_id is required")
		return
	}
	// Guard the spot's existence explicitly so a bad spot id is a clean 404 rather
	// than a silent no-op (the UPDATE targets the vehicle, not the spot).
	var exists bool
	if err := h.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM spots WHERE id=$1)`, spotID).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "spot not found")
		return
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE vehicles SET spot_id=$1, updated_at=now() WHERE id=$2`, spotID, req.VehicleID)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "this spot is already occupied")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not assign vehicle")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "vehicle not found")
		return
	}
	h.audit(r, "update", "spot", spotID, "assigned vehicle to spot")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// UnassignSpotVehicle frees whichever Gefährt currently stands on the spot (editor).
func (h *Handler) UnassignSpotVehicle(w http.ResponseWriter, r *http.Request) {
	spotID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE vehicles SET spot_id=NULL, updated_at=now() WHERE spot_id=$1`, spotID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not unassign vehicle")
		return
	}
	h.audit(r, "update", "spot", spotID, "freed spot")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// unassignedVehicle is the projection for the "nicht platziert" palette — it
// carries the physical dimensions so the planner can draw the footprint and run
// its fit checks the moment a Gefährt is dragged onto the floor.
type unassignedVehicle struct {
	ID         int64    `json:"id"`
	Label      string   `json:"label"`
	Type       string   `json:"type"`
	PersonID   *int64   `json:"person_id,omitempty"`
	PersonName string   `json:"person_name"`
	LengthM    *float64 `json:"length_m,omitempty"`
	WidthM     *float64 `json:"width_m,omitempty"`
	HeightM    *float64 `json:"height_m,omitempty"`
	WeightT    *float64 `json:"weight_t,omitempty"`
}

// ListUnassignedVehicles returns active Gefährte that stand on no spot yet — the
// candidates for the "nicht platziert" palette, with dimensions.
func (h *Handler) ListUnassignedVehicles(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT v.id,
		        COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), cat.name, 'Gefährt'),
		        COALESCE(cat.name, ''),
		        v.person_id, trim(p.first_name || ' ' || p.last_name),
		        v.length_m, v.width_m, v.height_m, v.weight_t
		   FROM vehicles v
		   LEFT JOIN categories cat ON cat.id = v.category_id
		   LEFT JOIN persons    p   ON p.id = v.person_id
		  WHERE v.spot_id IS NULL AND NOT v.archived
		  ORDER BY lower(COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), cat.name, 'Gefährt'))`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []unassignedVehicle{}
	for rows.Next() {
		var uv unassignedVehicle
		if err := rows.Scan(&uv.ID, &uv.Label, &uv.Type, &uv.PersonID, &uv.PersonName,
			&uv.LengthM, &uv.WidthM, &uv.HeightM, &uv.WeightT); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, uv)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// vehicleDimsRequest sets a Gefährt's physical measurements (all optional; null
// clears a value). Bounds mirror the DB CHECK constraint so a bad value is a
// clean 400 rather than a 500 from the constraint.
type vehicleDimsRequest struct {
	LengthM *float64 `json:"length_m"`
	WidthM  *float64 `json:"width_m"`
	HeightM *float64 `json:"height_m"`
	WeightT *float64 `json:"weight_t"`
}

// dimPos validates an optional length/width/height (meters): nil is ok (unknown,
// skips fit checks); otherwise it must be > 0 AFTER NUMERIC(6,2) rounding and <= max.
// This mirrors chk_vehicle_dims's "> 0" so zero or sub-centimeter values return a
// clean 400 instead of tripping the DB CHECK constraint (500).
func dimPos(v *float64, max float64) bool {
	return v == nil || (math.Round(*v*100)/100 > 0 && *v <= max)
}

// dimInRange validates the optional weight (tonnes): nil ok, else 0 ≤ v ≤ max
// (inclusive lower bound, matching chk_vehicle_dims's "weight_t >= 0").
func dimInRange(v *float64, max float64) bool {
	return v == nil || (*v >= 0 && *v <= max)
}

// SetVehicleDimensions updates a Gefährt's length/width/height/weight (editor).
func (h *Handler) SetVehicleDimensions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req vehicleDimsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !dimPos(req.LengthM, 60) || !dimPos(req.WidthM, 15) ||
		!dimPos(req.HeightM, 15) || !dimInRange(req.WeightT, 200) {
		writeError(w, http.StatusBadRequest, "dimension out of range")
		return
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE vehicles SET length_m=$1, width_m=$2, height_m=$3, weight_t=$4, updated_at=now() WHERE id=$5`,
		req.LengthM, req.WidthM, req.HeightM, req.WeightT, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update dimensions")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "vehicle not found")
		return
	}
	h.audit(r, "update", "vehicle", id, "updated dimensions")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
