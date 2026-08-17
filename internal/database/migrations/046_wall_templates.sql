-- Server-side wall templates (AR3): reusable planner wall layouts, shared across
-- devices/users instead of living only in the browser's localStorage.
CREATE TABLE IF NOT EXISTS wall_templates (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT        NOT NULL,
    walls      JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
