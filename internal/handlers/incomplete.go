package handlers

import (
	"encoding/json"
	"math"
	"net/http"
)

// incompleteVehicles fasst zusammen, welchen aktiven Gefährten Daten fehlen, die der
// Betrieb später braucht (Ausbau 13 und 8): Maße für die Passprüfung im Planer, ein
// Kennzeichen fürs Etikett, und Strom für ein Gefährt mit Ladebedarf.
type incompleteVehicles struct {
	NoDimensions int            `json:"no_dimensions"`
	NoPlate      int            `json:"no_plate"`
	PowerMissing []powerMissing `json:"power_missing"`
}

type powerMissing struct {
	VehicleID int64  `json:"vehicle_id"`
	Label     string `json:"label"`
	HallID    int64  `json:"hall_id"`
	HallName  string `json:"hall_name"`
}

// planBlock ist ein Rechteck aus der Planer-Geometrie: x/y = linke obere Ecke des
// ungedrehten Rechtecks, Drehung in Grad um die Mitte (wie geometry.js).
type planBlock struct {
	Kind string  `json:"kind"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
	Rot  float64 `json:"rot"`
}

// containsPoint ist pointInBlock aus web/static/js/geometry.js.
func (b planBlock) containsPoint(px, py float64) bool {
	cx, cy := b.X+b.W/2, b.Y+b.H/2
	r := b.Rot * math.Pi / 180
	c, s := math.Cos(r), math.Sin(r)
	dx, dy := px-cx, py-cy
	lx, ly := dx*c+dy*s, -dx*s+dy*c
	const e = 1e-9
	return math.Abs(lx) <= b.W/2+e && math.Abs(ly) <= b.H/2+e
}

// powerZones liest die Stromzonen einer Hallengeometrie. Unlesbare Geometrie heißt
// „keine Zonen“: dann wird nicht gewarnt, statt falsch zu warnen.
func powerZones(geometry []byte) []planBlock {
	var g struct {
		Excl []planBlock `json:"excl"`
	}
	if len(geometry) == 0 || json.Unmarshal(geometry, &g) != nil {
		return nil
	}
	var out []planBlock
	for _, b := range g.Excl {
		if b.Kind == "power" && b.W > 0 && b.H > 0 {
			out = append(out, b)
		}
	}
	return out
}

// IncompleteVehicles: GET /api/vehicles/incomplete. Gewarnt wird über Strom nur in
// Hallen, die überhaupt Stromzonen haben; ohne eingezeichnete Steckdosen wäre jedes
// Gefährt mit Ladebedarf ein Fehlalarm.
func (h *Handler) IncompleteVehicles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := incompleteVehicles{PowerMissing: []powerMissing{}}
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE length_m IS NULL OR width_m IS NULL),
		        count(*) FILTER (WHERE btrim(license_plate) = '')
		   FROM vehicles
		  WHERE NOT archived AND status IN ('stored', 'reserved')`).Scan(&out.NoDimensions, &out.NoPlate); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	rows, err := h.Pool.Query(ctx,
		`SELECT v.id, COALESCE(NULLIF(v.label, ''), NULLIF(v.license_plate, ''), c.name),
		        hl.id, hl.name, s.geometry, hl.geometry
		   FROM vehicles v
		   JOIN categories c ON c.id = v.category_id
		   JOIN spots s ON s.id = v.spot_id
		   JOIN halls hl ON hl.id = s.hall_id
		  WHERE NOT v.archived AND v.needs_power
		  ORDER BY hl.id, v.id`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	zonesByHall := map[int64][]planBlock{}
	for rows.Next() {
		var pm powerMissing
		var spotGeom, hallGeom []byte
		if err := rows.Scan(&pm.VehicleID, &pm.Label, &pm.HallID, &pm.HallName, &spotGeom, &hallGeom); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		zones, seen := zonesByHall[pm.HallID]
		if !seen {
			zones = powerZones(hallGeom)
			zonesByHall[pm.HallID] = zones
		}
		if len(zones) == 0 {
			continue
		}
		var spot planBlock
		if json.Unmarshal(spotGeom, &spot) != nil {
			continue
		}
		cx, cy := spot.X+spot.W/2, spot.Y+spot.H/2
		inside := false
		for _, z := range zones {
			if z.containsPoint(cx, cy) {
				inside = true
				break
			}
		}
		if !inside {
			out.PowerMissing = append(out.PowerMissing, pm)
		}
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
