-- 012: flat rates as dated agreement records ("Pauschale-Einträge").
-- A person can now have multiple flat-rate agreements over time, each with its
-- own window, covered vehicles and paid status — replacing the single per-person
-- flat rate. Non-destructive: the old persons.flat_rate_* columns and the
-- flatrate_paid_years table are left in place, and any existing flat rate is
-- migrated into one agreement row so billing continues unchanged.

CREATE TABLE IF NOT EXISTS flat_rate_periods (
    id         BIGSERIAL PRIMARY KEY,
    person_id  BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    amount     NUMERIC(12,2) NOT NULL,
    period     TEXT NOT NULL DEFAULT 'monthly' CHECK (period IN ('monthly', 'yearly')),
    start_date DATE NOT NULL,
    end_date   DATE,                       -- NULL = open-ended
    paid       BOOLEAN NOT NULL DEFAULT FALSE,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_flatrate_periods_person ON flat_rate_periods(person_id);

-- Which vehicles an agreement covers. No rows for an agreement => it covers ALL
-- of the person's vehicles.
CREATE TABLE IF NOT EXISTS flat_rate_period_vehicles (
    period_id  BIGINT NOT NULL REFERENCES flat_rate_periods(id) ON DELETE CASCADE,
    vehicle_id BIGINT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    PRIMARY KEY (period_id, vehicle_id)
);

-- Migrate an existing single flat rate into one agreement (covers all vehicles,
-- so no join rows). paid starts false; the old flatrate_paid_years data is kept.
INSERT INTO flat_rate_periods (person_id, amount, period, start_date, end_date)
SELECT id, flat_rate, flat_rate_period, flat_rate_start, flat_rate_end
FROM persons
WHERE flat_rate IS NOT NULL AND flat_rate > 0 AND flat_rate_start IS NOT NULL;
