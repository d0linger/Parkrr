package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Hallen-Auslastung als Fläche (Ausbauplan O1, Entscheidung vom 22.10.2026): belegte m²
// geteilt durch die nutzbare Fläche, dieselbe Zahl wie im Planer. Die nutzbare Fläche
// rechnet nur der Planer (Wände, Räume, Fahrstraßen); er legt sie als geometry.park in
// den Grundriss. Der Server zählt nur die belegten m² und teilt — eine zweite
// Flächenrechnung, die vom Planer abweichen könnte, gibt es so nicht.

// catKeyRules ist catKey aus app.js (gleiche Reihenfolge, gleiche Muster).
var catKeyRules = []struct {
	re  *regexp.Regexp
	key string
}{
	{regexp.MustCompile(`motorr|moped|roller|bike`), "Motorrad"},
	{regexp.MustCompile(`wohnmobil|reisemobil|camper`), "Wohnmobil"},
	{regexp.MustCompile(`wohnwagen|caravan`), "Wohnwagen"},
	{regexp.MustCompile(`boot|jetski|trailer`), "Boot / Trailer"},
	{regexp.MustCompile(`traktor|schlepper|landmasch`), "Traktor"},
	{regexp.MustCompile(`ladewagen`), "Ladewagen"},
	{regexp.MustCompile(`rückewagen|rueckewagen|forst`), "Rückewagen"},
	{regexp.MustCompile(`kipper|mulde`), "Kipper"},
	{regexp.MustCompile(`anhäng|anhaeng|häng|haeng`), "Anhänger"},
	{regexp.MustCompile(`transport|sprinter|kasten|\bvan\b|bus`), "Transporter"},
}

// catDef ist CATDEF aus app.js: Standard-Grundfläche (Länge, Breite) je Typ.
var catDef = map[string][2]float64{
	"PKW": {4.5, 1.9}, "Motorrad": {2.2, 0.9}, "Transporter": {5.4, 2.1}, "Wohnmobil": {7.0, 2.3},
	"Wohnwagen": {6.0, 2.2}, "Anhänger": {4.0, 1.8}, "Boot / Trailer": {6.0, 2.3}, "Traktor": {4.6, 2.2},
	"Ladewagen": {8.0, 2.5}, "Rückewagen": {7.5, 2.4}, "Kipper": {6.5, 2.4},
}

// catFoot ist catFoot aus app.js.
func catFoot(typ string) [2]float64 {
	t := strings.ToLower(typ)
	for _, r := range catKeyRules {
		if r.re.MatchString(t) {
			return catDef[r.key]
		}
	}
	return catDef["PKW"]
}

// jsNum ist num() aus app.js für einen Wert aus dem Grundriss: eine Zahl gilt, null
// zählt wie dort als 0, alles andere (fehlt, Text, Liste) nimmt den Ersatzwert.
func jsNum(raw []byte, fallback float64) float64 {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return fallback
	}
	if s == "null" {
		return 0
	}
	var v float64
	if err := json.Unmarshal([]byte(s), &v); err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	return v
}

// spotFootprint ist die Fläche, die der Planer für einen Platz zeichnet: Breite und
// Tiefe aus der Platzgeometrie, sonst die Maße des Gefährts, sonst der Typ-Standard
// (P.spots in app.js: num(g.w, s.length_m || catFoot(type)[0])).
func spotFootprint(geomW, geomH []byte, length, width *float64, typ string) float64 {
	foot := catFoot(typ)
	lw, ww := foot[0], foot[1]
	if length != nil && *length != 0 {
		lw = *length
	}
	if width != nil && *width != 0 {
		ww = *width
	}
	return jsNum(geomW, lw) * jsNum(geomH, ww)
}

// hallUsedArea summiert die Platzflächen je Halle, wie der Planer sie als „Belegt“
// zeigt: jeder Platz der Halle, auch einer, dessen Gefährt gerade fehlt.
func hallUsedArea(ctx context.Context, q pgx.Tx) (map[int64]float64, error) {
	rows, err := q.Query(ctx,
		`SELECT s.hall_id, s.geometry->'w', s.geometry->'h',
		        COALESCE(v.length_m, cat.default_length_m), COALESCE(v.width_m, cat.default_width_m),
		        COALESCE(cat.name, '')
		   FROM spots s
		   LEFT JOIN vehicles   v   ON v.spot_id = s.id
		   LEFT JOIN categories cat ON cat.id = v.category_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]float64{}
	for rows.Next() {
		var hall int64
		var gw, gh []byte
		var length, width *float64
		var typ string
		if err := rows.Scan(&hall, &gw, &gh, &length, &width, &typ); err != nil {
			return nil, err
		}
		out[hall] += spotFootprint(gw, gh, length, width, typ)
	}
	return out, rows.Err()
}

// minParkArea: darunter gilt eine Fläche als nicht gezeichnet (enc.area > 0.3 im Planer).
const minParkArea = 0.3

// areaPct ist die Auslastung in ganzen Prozent, nil ohne nutzbare Fläche.
func areaPct(used float64, park *float64) *int {
	if park == nil || *park < minParkArea {
		return nil
	}
	p := int(math.Round(used / *park * 100))
	return &p
}

// SetHallParkArea: PUT /api/halls/{id}/park-area — der Planer meldet seine nutzbare
// Fläche, wenn der gespeicherte Wert fehlt oder abweicht (Hallen, die vor O1
// gezeichnet wurden). Geschrieben wird nur dieser eine Schlüssel im Grundriss: ein
// Bearbeiter, der die Halle nur öffnet, überschreibt so nie die Wände eines anderen.
func (h *Handler) SetHallParkArea(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		ParkM2 *float64 `json:"park_m2"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.ParkM2 == nil || math.IsNaN(*in.ParkM2) || math.IsInf(*in.ParkM2, 0) || *in.ParkM2 < 0 || *in.ParkM2 > 1e6 {
		writeError(w, http.StatusBadRequest, "park_m2 muss zwischen 0 und 1000000 liegen")
		return
	}
	v := math.Round(*in.ParkM2*100) / 100
	var prev *float64
	err := h.Pool.QueryRow(r.Context(),
		`WITH prev AS (SELECT CASE WHEN jsonb_typeof(geometry->'park') = 'number' THEN (geometry->>'park')::float8 END AS park
		                 FROM halls WHERE id = $1)
		 UPDATE halls SET geometry = jsonb_set(geometry, '{park}', to_jsonb($2::float8))
		  WHERE id = $1 AND jsonb_typeof(geometry) = 'object'
		 RETURNING (SELECT park FROM prev)`, id, v).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "hall not found")
		return
	}
	if err != nil {
		serverError(w, r, "could not store park area", err)
		return
	}
	if prev == nil || math.Abs(*prev-v) >= 0.005 {
		h.auditChange(r, "update", "hall", id, "Parkfläche aus dem Planer: "+parkLabel(&v),
			diffFields(map[string]any{"geometry": parkLabel(prev)}, map[string]any{"geometry": parkLabel(&v)}))
	}
	writeJSON(w, http.StatusOK, map[string]any{"park_m2": v})
}

func parkLabel(v *float64) string {
	if v == nil {
		return "Parkfläche offen"
	}
	return "Parkfläche " + strings.Replace(strconv.FormatFloat(*v, 'f', 1, 64), ".", ",", 1) + " m²"
}
