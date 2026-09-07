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

// Standardmaße je Kategorie (Hundert 80): ein ungemessenes Gefährt erbt sie im
// Planer, eigene Maße gewinnen immer. Der Test prüft beide Regeln über die ECHTE
// Planer-Abfrage (ListUnassignedVehicles), nicht über die Tabellen.
func TestCategoryDefaultDimsFallThroughToPlanner(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	var pid, catID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Dims','Integration') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("person: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO categories (name, default_length_m, default_width_m, default_height_m, default_weight_t)
		 VALUES ('DimsTarif-'||clock_timestamp()::text, 7.5, 2.3, 2.9, 1.8) RETURNING id`).Scan(&catID); err != nil {
		t.Fatalf("category: %v", err)
	}
	mkVeh := func(label string) int64 {
		var vid int64
		if err := h.Pool.QueryRow(ctx,
			`INSERT INTO vehicles (person_id, category_id, status, label) VALUES ($1,$2,'stored',$3) RETURNING id`,
			pid, catID, label).Scan(&vid); err != nil {
			t.Fatalf("vehicle %s: %v", label, err)
		}
		return vid
	}
	unmeasured := mkVeh("DimsErbt")
	measured := mkVeh("DimsEigene")
	if _, err := h.Pool.Exec(ctx,
		`UPDATE vehicles SET length_m=4.1, width_m=1.9 WHERE id=$1`, measured); err != nil {
		t.Fatalf("set own dims: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_ = purgeExec(c, h.Pool, `DELETE FROM vehicles WHERE id IN ($1,$2)`, unmeasured, measured)
		_, _ = h.Pool.Exec(c, `DELETE FROM categories WHERE id=$1`, catID)
		_ = purgeExec(c, h.Pool, `DELETE FROM persons WHERE id=$1`, pid)
	})

	rec := httptest.NewRecorder()
	h.ListUnassignedVehicles(rec, httptest.NewRequest(http.MethodGet, "/api/planner/unassigned", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unassigned: %d %s", rec.Code, rec.Body.String())
	}
	var list []struct {
		ID      int64    `json:"id"`
		LengthM *float64 `json:"length_m"`
		WidthM  *float64 `json:"width_m"`
		HeightM *float64 `json:"height_m"`
		WeightT *float64 `json:"weight_t"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byID := map[int64]int{}
	for i := range list {
		byID[list[i].ID] = i
	}

	// Ungemessen → erbt die Vorgabe der Kategorie.
	if i, ok := byID[unmeasured]; !ok {
		t.Fatal("das ungemessene Gefährt fehlt in der Palette")
	} else {
		v := list[i]
		if v.LengthM == nil || *v.LengthM != 7.5 || v.WidthM == nil || *v.WidthM != 2.3 ||
			v.HeightM == nil || *v.HeightM != 2.9 || v.WeightT == nil || *v.WeightT != 1.8 {
			t.Errorf("Vorgabe nicht geerbt: %+v", v)
		}
	}
	// Eigene Maße gewinnen — auch dort, wo nur EINZELNE gesetzt sind, fällt der
	// Rest auf die Vorgabe zurück (COALESCE je Spalte, nicht je Zeile).
	if i, ok := byID[measured]; !ok {
		t.Fatal("das gemessene Gefährt fehlt in der Palette")
	} else {
		v := list[i]
		if v.LengthM == nil || *v.LengthM != 4.1 || v.WidthM == nil || *v.WidthM != 1.9 {
			t.Errorf("eigene Maße wurden überstimmt: %+v", v)
		}
		if v.HeightM == nil || *v.HeightM != 2.9 {
			t.Errorf("fehlende Höhe muss aus der Vorgabe kommen: %+v", v)
		}
	}
}

// Die Grenzen der Vorgabe entsprechen denen des Gefährt-Endpunkts: was kein
// Gefährt tragen dürfte, darf auch keine Vorgabe sein.
func TestCategoryDefaultDimsValidated(t *testing.T) {
	h := testHandler(t)
	tooLong := 61.0
	body, _ := json.Marshal(map[string]any{
		"name": "DimsZuLang-" + t.Name(), "default_monthly_cost": 10, "default_yearly_cost": 100,
		"default_length_m": tooLong,
	})
	rec := httptest.NewRecorder()
	h.CreateCategory(rec, httptest.NewRequest(http.MethodPost, "/api/categories", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("61 m Standardlänge: %d, erwartet 400 — %s", rec.Code, rec.Body.String())
	}
	if rec.Code == http.StatusCreated {
		var c struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &c)
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM categories WHERE id=$1`, c.ID)
		_ = strconv.Itoa(0)
	}
}
