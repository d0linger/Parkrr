-- Fix 1: a Pauschale/Nebenkosten sub-period marked "bezahlt" now books a REAL
-- Zahlungseingang (a payments row), not merely an off-book flag
-- (flat_rate_period_payments / recurring_charges.paid_periods). Historically that
-- money reduced the balance correctly but never appeared in the payments list or
-- audit trail — "als bezahlt markiert, jedoch keine Zahlung vermerkt". These auto
-- rows carry the settled position's identity (kind, ref, period) so un-toggle
-- removes exactly the one it created and the balance never double-counts.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS settles_kind   TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS settles_ref    BIGINT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS settles_period TEXT;

-- At most one payment per settled sub-period. Partial index: only the
-- period-settlement rows participate; every other payment leaves these columns
-- NULL and is unaffected.
CREATE UNIQUE INDEX IF NOT EXISTS uq_payments_settles
    ON payments (settles_kind, settles_ref, settles_period)
    WHERE settles_kind IS NOT NULL;
