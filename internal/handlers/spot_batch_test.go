package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// mkHallWithSpots legt Garage + Halle + n Plätze an und räumt alles wieder ab.
func mkHallWithSpots(t *testing.T, h *Handler, n int) (hallID int64, spotIDs []int64) {
	t.Helper()
	ctx := context.Background()
	var garageID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO garages (name) VALUES ('BatchGarage-'||clock_timestamp()::text) RETURNING id`).Scan(&garageID); err != nil {
		t.Fatalf("garage: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO halls (garage_id, name) VALUES ($1, 'BatchHalle') RETURNING id`, garageID).Scan(&hallID); err != nil {
		t.Fatalf("hall: %v", err)
	}
	for i := 0; i < n; i++ {
		var sid int64
		if err := h.Pool.QueryRow(ctx,
			`INSERT INTO spots (hall_id, label, geometry) VALUES ($1, $2, '{"x":0,"y":0}') RETURNING id`,
			hallID, fmt.Sprintf("B-%d", i)).Scan(&sid); err != nil {
			t.Fatalf("spot %d: %v", i, err)
		}
		spotIDs = append(spotIDs, sid)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.Pool.Exec(c, `DELETE FROM spots WHERE hall_id=$1`, hallID)
		_, _ = h.Pool.Exec(c, `DELETE FROM halls WHERE id=$1`, hallID)
		_, _ = h.Pool.Exec(c, `DELETE FROM garages WHERE id=$1`, garageID)
	})
	return hallID, spotIDs
}

func batchPut(t *testing.T, h *Handler, hallID int64, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/api/halls/"+strconv.FormatInt(hallID, 10)+"/spots/geometry", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", strconv.FormatInt(hallID, 10))
	rec := httptest.NewRecorder()
	h.BatchUpdateSpotGeometry(rec, req)
	return rec
}

// Der Batch schreibt alle Geometrien in einer Transaktion (Hundert 74).
func TestBatchSpotGeometryAppliesAll(t *testing.T) {
	h := testHandler(t)
	hallID, ids := mkHallWithSpots(t, h, 3)
	spots := make([]map[string]any, 0, len(ids))
	for i, id := range ids {
		spots = append(spots, map[string]any{"id": id, "geometry": map[string]any{"x": float64(i + 1), "y": 2.5, "w": 5, "h": 2, "rot": 0}})
	}
	rec := batchPut(t, h, hallID, map[string]any{"spots": spots})
	if rec.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", rec.Code, rec.Body.String())
	}
	for i, id := range ids {
		var geom []byte
		if err := h.Pool.QueryRow(context.Background(), `SELECT geometry FROM spots WHERE id=$1`, id).Scan(&geom); err != nil {
			t.Fatalf("read spot: %v", err)
		}
		var g struct{ X float64 }
		_ = json.Unmarshal(geom, &g)
		if g.X != float64(i+1) {
			t.Errorf("spot %d: x=%v, erwartet %d", id, g.X, i+1)
		}
	}
}

// Alles-oder-nichts: enthält der Batch eine fremde oder fehlende id, wird NICHTS
// übernommen — eine halb angewandte Anordnung ist genau der Zustand, den der
// Batch verhindern soll.
func TestBatchSpotGeometryIsAtomic(t *testing.T) {
	h := testHandler(t)
	hallID, ids := mkHallWithSpots(t, h, 2)
	otherHall, otherIDs := mkHallWithSpots(t, h, 1)
	_ = otherHall

	rec := batchPut(t, h, hallID, map[string]any{"spots": []map[string]any{
		{"id": ids[0], "geometry": map[string]any{"x": 9.9}},
		{"id": otherIDs[0], "geometry": map[string]any{"x": 8.8}}, // fremde Halle!
	}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("fremde id im Batch: %d, erwartet 404 — %s", rec.Code, rec.Body.String())
	}
	// Der eigene Platz darf NICHT verändert sein (Rollback).
	var geom []byte
	if err := h.Pool.QueryRow(context.Background(), `SELECT geometry FROM spots WHERE id=$1`, ids[0]).Scan(&geom); err != nil {
		t.Fatalf("read: %v", err)
	}
	var g struct{ X float64 }
	_ = json.Unmarshal(geom, &g)
	if g.X == 9.9 {
		t.Error("der Batch hat trotz 404 teilweise geschrieben — er muss atomar sein")
	}
	// Und die fremde Halle blieb unangetastet.
	if err := h.Pool.QueryRow(context.Background(), `SELECT geometry FROM spots WHERE id=$1`, otherIDs[0]).Scan(&geom); err != nil {
		t.Fatalf("read other: %v", err)
	}
	_ = json.Unmarshal(geom, &g)
	if g.X == 8.8 {
		t.Error("der Batch hat über die Hallengrenze geschrieben")
	}
}

// Kaputte Eingaben: leer, doppelte id, ungültige Geometrie — alles 400.
func TestBatchSpotGeometryValidation(t *testing.T) {
	h := testHandler(t)
	hallID, ids := mkHallWithSpots(t, h, 1)
	cases := []struct {
		name string
		body any
	}{
		{"leer", map[string]any{"spots": []any{}}},
		{"doppelte id", map[string]any{"spots": []map[string]any{
			{"id": ids[0], "geometry": map[string]any{"x": 1}},
			{"id": ids[0], "geometry": map[string]any{"x": 2}},
		}}},
		{"ungültige id", map[string]any{"spots": []map[string]any{{"id": 0, "geometry": map[string]any{}}}}},
	}
	for _, tc := range cases {
		if rec := batchPut(t, h, hallID, tc.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, erwartet 400", tc.name, rec.Code)
		}
	}
}
