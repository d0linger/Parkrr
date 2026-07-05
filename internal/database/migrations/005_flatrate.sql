-- Person-level flat rate (Pauschale): one agreed amount per month or year that
-- covers all of a person's vehicles instead of per-vehicle billing.
ALTER TABLE persons ADD COLUMN IF NOT EXISTS flat_rate NUMERIC(12,2);
ALTER TABLE persons ADD COLUMN IF NOT EXISTS flat_rate_period TEXT NOT NULL DEFAULT 'monthly'
    CHECK (flat_rate_period IN ('monthly', 'yearly'));
ALTER TABLE persons ADD COLUMN IF NOT EXISTS flat_rate_start DATE;
ALTER TABLE persons ADD COLUMN IF NOT EXISTS flat_rate_end   DATE;
ALTER TABLE persons ADD COLUMN IF NOT EXISTS flat_rate_paid  BOOLEAN NOT NULL DEFAULT FALSE;
