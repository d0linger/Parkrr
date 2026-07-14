package handlers

import (
	"bytes"
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

// TestUpdateChargePaidResetOnRebind checks that editing a charge keeps its paid
// flag when the vehicle binding is unchanged, but resets it to open when the
// binding changes (a bound charge follows its vehicle, so the old flag is stale).
func TestUpdateChargePaidResetOnRebind(t *testing.T) {
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set; skipping charge update integration test")
	}
	ctx := context.Background()
	pool, err := database.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	catName := fmt.Sprintf("zzChgCat_%d", time.Now().UnixNano())
	var personID, catID, vehID, chargeID int64
	must := func(label string, e error) {
		if e != nil {
			t.Fatalf("%s: %v", label, e)
		}
	}
	must("person", pool.QueryRow(ctx,
		`INSERT INTO persons (first_name,last_name) VALUES ('Chg','Rebind') RETURNING id`).Scan(&personID))
	must("category", pool.QueryRow(ctx,
		`INSERT INTO categories (name, default_monthly_cost, default_yearly_cost) VALUES ($1,0,0) RETURNING id`, catName).Scan(&catID))
	must("vehicle", pool.QueryRow(ctx,
		`INSERT INTO vehicles (person_id, category_id, rate, billing_period, start_date, status, paid)
		 VALUES ($1,$2,0,'monthly',CURRENT_DATE,'stored',false) RETURNING id`, personID, catID).Scan(&vehID))
	must("charge", pool.QueryRow(ctx,
		`INSERT INTO charges (person_id, description, amount, quantity, charged_on, paid)
		 VALUES ($1,'Test',10,1,CURRENT_DATE,true) RETURNING id`, personID).Scan(&chargeID))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM charges WHERE id=$1`, chargeID)
		_, _ = pool.Exec(ctx, `DELETE FROM vehicles WHERE id=$1`, vehID)
		_, _ = pool.Exec(ctx, `DELETE FROM persons WHERE id=$1`, personID)
		_, _ = pool.Exec(ctx, `DELETE FROM categories WHERE id=$1`, catID)
	})

	h := New(pool)
	update := func(vehicleID *int64) {
		body := map[string]any{
			"person_id": personID, "description": "Test", "amount": 10, "quantity": 1,
			"charged_on": time.Now().Format("2006-01-02"),
		}
		if vehicleID != nil {
			body["vehicle_id"] = *vehicleID
		}
		buf, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/charges/x", bytes.NewReader(buf))
		req.SetPathValue("id", fmt.Sprint(chargeID))
		h.UpdateCharge(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("update status %d: %s", rec.Code, rec.Body.String())
		}
	}
	paid := func() bool {
		var p bool
		if e := pool.QueryRow(ctx, `SELECT paid FROM charges WHERE id=$1`, chargeID).Scan(&p); e != nil {
			t.Fatalf("read paid: %v", e)
		}
		return p
	}

	// Edit without changing the binding (stays standalone): paid stays true.
	update(nil)
	if !paid() {
		t.Errorf("paid should be preserved (true) when the binding is unchanged")
	}
	// Bind to the vehicle: binding changed => paid resets to open.
	update(&vehID)
	if paid() {
		t.Errorf("paid should reset to false when the vehicle binding changes")
	}
}
