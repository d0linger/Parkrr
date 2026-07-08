-- 013: lock the effective rate onto each vehicle (Option A).
-- Pricing now reads vehicles.rate instead of the live Tarif, so changing a Tarif
-- no longer re-prices existing (incl. ended/paid) vehicles. Backfill each
-- vehicle's rate from its current effective price so today's figures are
-- unchanged: the special price if set, otherwise the current Tarif default for
-- its billing period.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS rate NUMERIC(12,2) NOT NULL DEFAULT 0;

UPDATE vehicles v
SET rate = COALESCE(
        v.cost_override,
        CASE WHEN v.billing_period = 'yearly' THEN c.default_yearly_cost
             ELSE c.default_monthly_cost END,
        0)
FROM categories c
WHERE c.id = v.category_id AND v.rate = 0;
