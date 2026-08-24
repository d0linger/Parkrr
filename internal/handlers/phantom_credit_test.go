package handlers

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestSyncTogglePaymentTrimsLostBoundCharge exercises the H-03 fix: when a bound
// charge is already claimed by another payment (the concurrent manual settlement
// the per-item advisory lock does not cover), toggling the vehicle paid must NOT
// re-claim or count it. The auto-payment is trimmed to what it actually won, so its
// amount never exceeds the sum of its allocations (no phantom credit).
func TestSyncTogglePaymentTrimsLostBoundCharge(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(1).Format("2006-01-02"))

	// A €20 charge bound to the vehicle.
	var chargeID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO charges (person_id, vehicle_id, description, amount, quantity)
		 VALUES ($1,$2,'Frostschutz',20,1) RETURNING id`, pid, vid).Scan(&chargeID); err != nil {
		t.Fatalf("insert charge: %v", err)
	}

	// Another payment already CLAIMS that charge.
	var otherPay int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO payments (person_id, amount, method, auto) VALUES ($1,20,'bar',false) RETURNING id`,
		pid).Scan(&otherPay); err != nil {
		t.Fatalf("insert other payment: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1,'charge',$2,20)`,
		otherPay, chargeID); err != nil {
		t.Fatalf("pre-claim charge: %v", err)
	}

	// Toggle the vehicle paid: rent 30 + the (already-claimed) bound charge 20.
	if err := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		return h.syncTogglePaymentTx(ctx, tx, "vehicle", vid, pid, true, 30, []boundCharge{{id: chargeID, total: 20}})
	}); err != nil {
		t.Fatalf("syncTogglePaymentTx: %v", err)
	}

	// The vehicle's auto-payment must exist and be trimmed to 30 (rent only), NOT 50 —
	// the bound charge was lost to the other payment.
	var amount float64
	if err := h.Pool.QueryRow(ctx,
		`SELECT amount FROM payments
		  WHERE auto AND id IN (SELECT payment_id FROM payment_allocations WHERE kind='vehicle' AND ref_id=$1)`,
		vid).Scan(&amount); err != nil {
		t.Fatalf("query auto-payment: %v", err)
	}
	if amount != 30 {
		t.Errorf("auto-payment amount = %.2f, want 30 (trimmed; the 20 charge was already claimed)", amount)
	}

	// The charge keeps exactly ONE allocation — no double-claim.
	var allocN int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM payment_allocations WHERE kind='charge' AND ref_id=$1`, chargeID).Scan(&allocN); err != nil {
		t.Fatalf("count charge allocations: %v", err)
	}
	if allocN != 1 {
		t.Errorf("charge allocation count = %d, want 1 (no double-claim)", allocN)
	}

	// Conservation: no payment allocates more than its own amount (phantom credit).
	var overallocated int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM payments p
		  WHERE p.amount < COALESCE((SELECT SUM(amount) FROM payment_allocations WHERE payment_id=p.id),0) - 0.005`).
		Scan(&overallocated); err != nil {
		t.Fatalf("conservation check: %v", err)
	}
	if overallocated != 0 {
		t.Errorf("%d payment(s) allocate more than their amount (phantom credit)", overallocated)
	}
}
