package handlers

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/preining/parkrr/internal/database"
)

// TestOutstandingByPersonMatchesStats locks the invariant that the batched
// persons-list balance (OutstandingByPerson) agrees with the per-person
// PersonStats.Balance — the two share the money helpers and must not drift.
// A person with an unpaid recurring charge shows the open balance; settling it
// drops them out of the map's positive set (balance ≤ 0).
func TestOutstandingByPersonMatchesStats(t *testing.T) {
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set; skipping outstanding integration test")
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

	var personID, rcID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Owe','Integration') RETURNING id`).Scan(&personID); err != nil {
		t.Fatalf("person: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO recurring_charges (person_id, description, amount, period, start_date)
		 VALUES ($1,'Strom',100000,'monthly',CURRENT_DATE - 40) RETURNING id`, personID).Scan(&rcID); err != nil {
		t.Fatalf("recurring: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM recurring_charges WHERE id=$1`, rcID)
		_, _ = pool.Exec(ctx, `DELETE FROM persons WHERE id=$1`, personID)
	})

	h := New(pool)
	personBalance := func() float64 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/persons/x/stats", nil)
		req.SetPathValue("id", strconv.FormatInt(personID, 10))
		h.PersonStats(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("stats status %d: %s", rec.Code, rec.Body.String())
		}
		var s personStatsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
			t.Fatalf("decode stats: %v", err)
		}
		return s.Balance
	}
	outstanding := func() map[int64]float64 {
		rec := httptest.NewRecorder()
		h.OutstandingByPerson(rec, httptest.NewRequest(http.MethodGet, "/api/persons/outstanding", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("outstanding status %d: %s", rec.Code, rec.Body.String())
		}
		var m map[int64]float64
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode outstanding: %v", err)
		}
		return m
	}

	bal := personBalance()
	if bal < 100000 {
		t.Fatalf("expected open balance ≥ 100000 while unpaid, got %.2f", bal)
	}
	// PersonStats and OutstandingByPerson are separate handler calls, each taking
	// its own time.Now(); a monthly charge accrues continuously (CostInRange uses
	// fractional days), so between the two calls the amount grows a hair and can
	// land on opposite sides of a rounding cent. Allow a few cents of that timing
	// noise — a real divergence between the two views would be euro-scale.
	if got := outstanding()[personID]; math.Abs(got-bal) > 0.05 {
		t.Errorf("outstanding %.2f != stats balance %.2f (beyond timing noise)", got, bal)
	}

	// P2.2: money truth = recorded payments. Settling = recording a payment of the
	// open amount (marking the toggle no longer moves the money). The tolerance
	// covers the same continuous-accrual timing noise as the divergence check.
	if _, err := pool.Exec(ctx, `INSERT INTO payments (person_id, amount, method) VALUES ($1,$2,'ueberweisung')`, personID, bal); err != nil {
		t.Fatalf("record payment: %v", err)
	}
	if bal := personBalance(); math.Abs(bal) > 0.05 {
		t.Errorf("expected settled balance ~0 in stats, got %.2f", bal)
	}
	if got := outstanding()[personID]; math.Abs(got) > 0.05 {
		t.Errorf("expected settled balance ~0 in outstanding, got %.2f", got)
	}
}
