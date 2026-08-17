-- Daily occupancy snapshots (FE4): one row per calendar day, upserted whenever the
-- dashboard occupancy KPI is loaded, so the dashboard can show a placement trend
-- over time without a separate scheduler.
CREATE TABLE IF NOT EXISTS occupancy_snapshots (
    day      DATE        PRIMARY KEY,
    placed   INTEGER     NOT NULL,
    active   INTEGER     NOT NULL,
    taken_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
