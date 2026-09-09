package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Der Belegungs-Trigger (Migration 061) historisiert JEDEN Weg einer
// Umplatzierung: zuweisen, umziehen, entfernen, Gefährt löschen. Der Test fährt
// den Lebenszyklus über direkte spot_id-Updates (so schreiben ihn alle Handler)
// und prüft die Zeiträume aus beiden Richtungen (je Platz, je Gefährt).
func TestSpotOccupancyHistoryTracksMoves(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	hallID, spotIDs := mkHallWithSpots(t, h, 2)
	_ = hallID
	vid := mkPhotoVehicle(t, h)
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM spot_occupancy_history WHERE vehicle_id=$1`, vid)
	})

	set := func(spot any) {
		t.Helper()
		if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET spot_id=$1 WHERE id=$2`, spot, vid); err != nil {
			t.Fatalf("set spot %v: %v", spot, err)
		}
	}
	set(spotIDs[0]) // zuweisen
	set(spotIDs[1]) // umziehen
	set(nil)        // entfernen

	hist := func(path, id string) []occupancyEntry {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		if path[:10] == "/api/spots" {
			h.SpotHistory(rec, req)
		} else {
			h.VehicleSpotHistory(rec, req)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		var out []occupancyEntry
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	// Je Gefährt: zwei abgeschlossene Zeiträume, keiner offen.
	vh := hist("/api/vehicles/x/spot-history", strconv.FormatInt(vid, 10))
	if len(vh) != 2 {
		t.Fatalf("%d Zeiträume fürs Gefährt, erwartet 2: %+v", len(vh), vh)
	}
	for _, e := range vh {
		if e.EndedAt == nil {
			t.Errorf("nach dem Entfernen darf kein Zeitraum offen sein: %+v", e)
		}
		if e.VehicleLabel == "" || e.SpotLabel == "" {
			t.Errorf("Labels müssen eingefroren sein: %+v", e)
		}
	}
	// Je Platz: Platz 0 hat genau den ersten Zeitraum.
	sh := hist("/api/spots/x/history", strconv.FormatInt(spotIDs[0], 10))
	if len(sh) != 1 || sh[0].VehicleID != vid {
		t.Fatalf("Platz-0-Historie falsch: %+v", sh)
	}

	// Gefährt löschen, während es platziert ist: der offene Zeitraum wird geschlossen.
	vid2 := mkPhotoVehicle(t, h)
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET spot_id=$1 WHERE id=$2`, spotIDs[0], vid2); err != nil {
		t.Fatalf("place vid2: %v", err)
	}
	if err := purgeExec(ctx, h.Pool, `DELETE FROM vehicles WHERE id=$1`, vid2); err != nil {
		t.Fatalf("delete vid2: %v", err)
	}
	var open int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM spot_occupancy_history WHERE vehicle_id=$1 AND ended_at IS NULL`, vid2).Scan(&open); err != nil {
		t.Fatalf("count open: %v", err)
	}
	if open != 0 {
		t.Errorf("das Löschen ließ %d offene Zeiträume zurück", open)
	}
	_, _ = h.Pool.Exec(ctx, `DELETE FROM spot_occupancy_history WHERE vehicle_id=$1`, vid2)
}
