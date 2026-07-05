-- Per-year paid status for the person flat rate (Pauschale): each billing year
-- can be marked open/paid independently. A row here means "year is paid".
CREATE TABLE IF NOT EXISTS flatrate_paid_years (
    person_id  BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    year       INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (person_id, year)
);

-- Carry the previous single "flat_rate_paid" flag over to the current year.
INSERT INTO flatrate_paid_years (person_id, year)
SELECT id, EXTRACT(year FROM CURRENT_DATE)::int
FROM persons
WHERE flat_rate_paid = TRUE AND flat_rate IS NOT NULL
ON CONFLICT DO NOTHING;
