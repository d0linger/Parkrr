package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/database"
)

// TestOverviewTopOutstanding seeds a person with a large unpaid balance and
// asserts the dashboard surfaces them at the top of top_outstanding.
func TestOverviewTopOutstanding(t *testing.T) {
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set; skipping overview integration test")
	}
	ctx := context.Background()
	pool, err := database.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Registered first so it runs LAST (Cleanup is LIFO) — the data cleanup
	// below must still have an open pool.
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Unique category name so a leftover from an aborted run can't collide.
	catName := fmt.Sprintf("zzIntegrationCat_%d", time.Now().UnixNano())

	// One expensive, unpaid, stored vehicle started a month ago => by far the
	// largest open balance, so this person must land at rank 1.
	var personID, catID, vehID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Owe','Integration') RETURNING id`).Scan(&personID); err != nil {
		t.Fatalf("person: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO categories (name, default_monthly_cost, default_yearly_cost)
		 VALUES ($1, 0, 0) RETURNING id`, catName).Scan(&catID); err != nil {
		t.Fatalf("category: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO vehicles (person_id, category_id, rate, billing_period, start_date, status, paid)
		 VALUES ($1,$2,999999,'monthly',CURRENT_DATE - 30,'stored',false) RETURNING id`,
		personID, catID).Scan(&vehID); err != nil {
		t.Fatalf("vehicle: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM vehicles WHERE id=$1`, vehID)
		_ = purgeExec(ctx, pool, `DELETE FROM persons WHERE id=$1`, personID)
		_, _ = pool.Exec(ctx, `DELETE FROM categories WHERE id=$1`, catID)
	})

	rec := httptest.NewRecorder()
	New(pool).Overview(rec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("overview status %d: %s", rec.Code, rec.Body.String())
	}
	var ov overviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(ov.TopOutstanding) == 0 {
		t.Fatal("expected at least one entry in top_outstanding")
	}
	top := ov.TopOutstanding[0]
	if top.PersonID != personID {
		t.Errorf("expected big-balance person %d at rank 1, got %d (%q, %.2f)",
			personID, top.PersonID, top.Name, top.Outstanding)
	}
	if top.Outstanding <= 0 {
		t.Errorf("expected positive outstanding, got %.2f", top.Outstanding)
	}
	if top.Name != "Owe Integration" {
		t.Errorf("expected name %q, got %q", "Owe Integration", top.Name)
	}
	// Descending order invariant.
	for i := 1; i < len(ov.TopOutstanding); i++ {
		if ov.TopOutstanding[i-1].Outstanding < ov.TopOutstanding[i].Outstanding {
			t.Errorf("top_outstanding not sorted descending at %d", i)
		}
	}
}
