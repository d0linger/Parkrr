-- Stellplatz-/Hallen-Verwaltung (Garagenplaner): a purely ORGANISATIONAL spatial
-- layer over the billing model. It records WHERE a Gefährt physically stands and
-- never touches pricing, agreements or invoicing — so it sits entirely outside
-- the BAO money perimeter. Three levels: garage → hall → spot, plus a 1:1
-- (optional) link from a vehicle to a spot.

CREATE TABLE IF NOT EXISTS garages (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    sort_order INT  NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Case-insensitive unique garage names (so "Nord" and "nord" don't coexist).
CREATE UNIQUE INDEX IF NOT EXISTS uq_garages_name ON garages (lower(name));

CREATE TABLE IF NOT EXISTS halls (
    id         BIGSERIAL PRIMARY KEY,
    garage_id  BIGINT NOT NULL REFERENCES garages(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    -- Freeform floor geometry the planner owns: polygon boundary, scale
    -- calibration, exclusion zones, view transform. Opaque JSON to the backend —
    -- validated only as well-formed JSON and size-capped by the handler.
    geometry   JSONB NOT NULL DEFAULT '{}'::jsonb,
    sort_order INT  NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_halls_garage ON halls (garage_id);
-- Hall names are unique within their garage, case-insensitively.
CREATE UNIQUE INDEX IF NOT EXISTS uq_halls_garage_name ON halls (garage_id, lower(name));

CREATE TABLE IF NOT EXISTS spots (
    id         BIGSERIAL PRIMARY KEY,
    hall_id    BIGINT NOT NULL REFERENCES halls(id) ON DELETE CASCADE,
    label      TEXT NOT NULL,
    -- Block geometry on the hall floor (x/y/w/h/rotation or polygon). Opaque JSON,
    -- same contract as halls.geometry.
    geometry   JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_spots_hall ON spots (hall_id);
-- Spot labels are unique within their hall, case-insensitively.
CREATE UNIQUE INDEX IF NOT EXISTS uq_spots_hall_label ON spots (hall_id, lower(label));

-- 1:1 (optional) Gefährt ↔ Stellplatz. The link lives on the vehicle — a Gefährt
-- stands on at most one spot. ON DELETE SET NULL frees the vehicle when its spot
-- is removed; the partial UNIQUE index enforces at most one Gefährt per spot
-- (NULLs are exempt, so any number of vehicles may have no spot).
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS spot_id BIGINT
    REFERENCES spots(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_vehicles_spot ON vehicles (spot_id) WHERE spot_id IS NOT NULL;
