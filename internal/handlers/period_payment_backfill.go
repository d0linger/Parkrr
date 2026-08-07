package handlers

import (
	"context"
	"time"

	"github.com/preining/parkrr/internal/models"
)

// BackfillPeriodPayments books the real Zahlungseingang (payments row) for every
// Pauschale/Nebenkosten sub-period that was settled through the old off-book flag
// before migration 036, so historical settlements also show in the payments list —
// not only ones toggled after the upgrade. It is idempotent (each insert is guarded
// by NOT EXISTS on the (kind, ref, period) identity) and safe to run on every start:
// only still-running whole periods and the standing master "paid" flag stay off-book
// (no final money yet), matching the live toggle path. A reversed period payment is
// left reversed — the guard sees the existing row and skips it.
func (h *Handler) BackfillPeriodPayments(ctx context.Context) error {
	now := time.Now()
	agByPerson, err := h.loadAllAgreements(ctx, 0)
	if err != nil {
		return err
	}
	for pid, ags := range agByPerson {
		for i := range ags {
			if err := h.backfillAccrualPayments(ctx, pid, "agreement", ags[i].ID, ags[i], now); err != nil {
				return err
			}
		}
	}
	recurByPerson, err := h.loadAllRecurringCharges(ctx, now)
	if err != nil {
		return err
	}
	for pid, rcs := range recurByPerson {
		for i := range rcs {
			if err := h.backfillAccrualPayments(ctx, pid, "recurring", rcs[i].ID, rcs[i].AsPeriod(), now); err != nil {
				return err
			}
		}
	}
	return nil
}

// backfillAccrualPayments books the missing real payments for one accrual's
// explicitly settled sub-periods (whole via PaidPeriods, partial via PaidFixed).
func (h *Handler) backfillAccrualPayments(ctx context.Context, personID int64, kind string, refID int64, p models.FlatRatePeriod, now time.Time) error {
	seen := map[string]bool{}
	record := func(key string, amount *float64) error {
		if seen[key] {
			return nil
		}
		seen[key] = true
		amt, ok := periodPaymentAmount(p, key, amount, now)
		if !ok || amt < 0.005 {
			return nil
		}
		_, err := h.Pool.Exec(ctx,
			`INSERT INTO payments (person_id, amount, method, note, auto, settles_kind, settles_ref, settles_period)
			 SELECT $1,$2,'bar',$3,true,$4,$5,$6
			 WHERE NOT EXISTS (SELECT 1 FROM payments WHERE settles_kind=$4 AND settles_ref=$5 AND settles_period=$6)`,
			personID, amt, periodPaymentNote(kind, key), kind, refID, key)
		return err
	}
	// Partials first so a key that (defensively) appears in both wins as a partial.
	for key, amt := range p.PaidFixed {
		a := amt
		if err := record(key, &a); err != nil {
			return err
		}
	}
	for _, key := range p.PaidPeriods {
		if err := record(key, nil); err != nil {
			return err
		}
	}
	return nil
}
