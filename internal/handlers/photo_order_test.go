package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Fotoreihenfolge + Titelbild (Hundert 58): der Client schickt die vollständige
// id-Liste, Position 0 ist das Titelbild, und der Planer zeigt genau dieses.
func TestPhotoReorderSetsTitleImage(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	vid := mkPhotoVehicle(t, h)

	up := func(name string) int64 {
		if rec := uploadPhotoReq(t, h, vid, "photo", name, tinyJPEG(t)); rec.Code != http.StatusCreated {
			t.Fatalf("upload %s: %d", name, rec.Code)
		}
		var id int64
		if err := h.Pool.QueryRow(ctx,
			`SELECT id FROM vehicle_photos WHERE vehicle_id=$1 AND filename=$2`, vid, name).Scan(&id); err != nil {
			t.Fatalf("id %s: %v", name, err)
		}
		return id
	}
	a, b, c := up("a.jpg"), up("b.jpg"), up("c.jpg")

	order := func(ids []int64) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"ids": ids})
		req := httptest.NewRequest(http.MethodPut, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/photos/order", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", strconv.FormatInt(vid, 10))
		rec := httptest.NewRecorder()
		h.ReorderPhotos(rec, req)
		return rec
	}

	// c wird Titelbild.
	if rec := order([]int64{c, a, b}); rec.Code != http.StatusOK {
		t.Fatalf("reorder: %d %s", rec.Code, rec.Body.String())
	}
	// ListPhotos liefert in der neuen Reihenfolge.
	lreq := httptest.NewRequest(http.MethodGet, "/x", nil)
	lreq.SetPathValue("id", strconv.FormatInt(vid, 10))
	lrec := httptest.NewRecorder()
	h.ListPhotos(lrec, lreq)
	var list []photoMeta
	_ = json.Unmarshal(lrec.Body.Bytes(), &list)
	if len(list) != 3 || list[0].ID != c || list[1].ID != a || list[2].ID != b {
		t.Fatalf("Reihenfolge nach Reorder falsch: %+v", list)
	}
	// Und der Planer-Pfad (erste-Foto-Subquery) zeigt das Titelbild.
	var plannerPhoto int64
	if err := h.Pool.QueryRow(ctx,
		`SELECT vp.id FROM vehicle_photos vp WHERE vp.vehicle_id=$1 ORDER BY vp.sort_order, vp.created_at DESC, vp.id LIMIT 1`,
		vid).Scan(&plannerPhoto); err != nil {
		t.Fatalf("planner photo: %v", err)
	}
	if plannerPhoto != c {
		t.Errorf("der Planer zeigt %d, erwartet das Titelbild %d", plannerPhoto, c)
	}

	// Unvollständige oder fremde Listen sind ein 409 — nichts wird halb sortiert.
	if rec := order([]int64{a, b}); rec.Code != http.StatusConflict {
		t.Errorf("Teil-Liste: %d, erwartet 409", rec.Code)
	}
	if rec := order([]int64{a, b, 99999999}); rec.Code != http.StatusConflict {
		t.Errorf("fremde id: %d, erwartet 409", rec.Code)
	}
	// Und die Reihenfolge blieb dabei unangetastet.
	lrec2 := httptest.NewRecorder()
	h.ListPhotos(lrec2, lreq)
	var list2 []photoMeta
	_ = json.Unmarshal(lrec2.Body.Bytes(), &list2)
	if list2[0].ID != c {
		t.Error("eine abgelehnte Liste hat die Reihenfolge trotzdem verändert")
	}
}
