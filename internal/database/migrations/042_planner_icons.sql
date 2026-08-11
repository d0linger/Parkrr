-- Custom Garagenplaner icons ("tags"): user-uploaded top-view images that become
-- selectable per vehicle (vehicles.planner_symbol = 'custom:<id>') alongside the
-- built-in category symbols. Images are re-encoded to PNG/JPEG on upload (metadata
-- stripped) and served only as image/* with nosniff, so they are XSS-safe.
CREATE TABLE IF NOT EXISTS planner_icons (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    content_type TEXT NOT NULL,
    data         BYTEA NOT NULL,
    byte_size    INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_planner_icons_name ON planner_icons (lower(name));
