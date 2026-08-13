-- 017: optional partial amount on a per-period payment.
-- A payment row with amount = NULL means the whole sub-period is paid (prepaid /
-- "im Voraus", the existing behaviour). A non-NULL amount records a fixed partial
-- payment ("Teilbetrag") for the running period: only that amount is credited, so
-- the rest of the period keeps accruing as open. Additive — existing rows stay
-- NULL and bill exactly as before.
ALTER TABLE flat_rate_period_payments ADD COLUMN IF NOT EXISTS amount NUMERIC(12,2);
