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

// TestRecurringChargeInStats seeds a person with one dominating recurring charge
// and asserts PersonStats and the dashboard fold its accrued/paid amounts into
// the totals (open until paid, settled afterwards).
func TestRecurringChargeInStats(t *testing.T) {
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set; skipping recurring stats integration test")
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
		`INSERT INTO persons (first_name, last_name) VALUES ('Rec','Integration') RETURNING id`).Scan(&personID); err != nil {
		t.Fatalf("person: %v", err)
	}
	// 100000/month starting 40 days ago => at least one full month accrued and, by
	// its size, the largest open balance on the dashboard.
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
	personStats := func() personStatsResponse {
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
		return s
	}

	s := personStats()
	if len(s.RecurringCharges) != 1 {
		t.Fatalf("expected 1 recurring charge in stats, got %d", len(s.RecurringCharges))
	}
	if s.TotalCharges < 100000 {
		t.Errorf("recurring accrued not folded into total_charges: %.2f", s.TotalCharges)
	}
	if s.TotalPaid != 0 {
		t.Errorf("expected 0 paid, got %.2f", s.TotalPaid)
	}
	if math.Abs(s.Balance-s.TotalCharges) > 0.01 {
		t.Errorf("balance %.2f should equal charges %.2f while unpaid", s.Balance, s.TotalCharges)
	}

	// The dashboard surfaces the open recurring balance.
	orec := httptest.NewRecorder()
	h.Overview(orec, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if orec.Code != http.StatusOK {
		t.Fatalf("overview status %d: %s", orec.Code, orec.Body.String())
	}
	var ov overviewResponse
	if err := json.Unmarshal(orec.Body.Bytes(), &ov); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	found := false
	for _, o := range ov.TopOutstanding {
		if o.PersonID == personID {
			found = true
			if o.Outstanding < 100000 {
				t.Errorf("recurring accrual missing from outstanding: %.2f", o.Outstanding)
			}
		}
	}
	if !found {
		t.Errorf("person with recurring charge not in top_outstanding")
	}

	// Settling the whole charge clears the balance.
	if _, err := pool.Exec(ctx, `UPDATE recurring_charges SET paid=true WHERE id=$1`, rcID); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	s2 := personStats()
	if math.Abs(s2.TotalPaid-s2.TotalCharges) > 0.01 {
		t.Errorf("paid %.2f should equal charges %.2f after settling", s2.TotalPaid, s2.TotalCharges)
	}
	if s2.Balance > 0.01 {
		t.Errorf("expected settled balance ~0, got %.2f", s2.Balance)
	}
}
