-- Initial schema for Parkrr.

CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    email         TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    is_admin      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS persons (
    id         BIGSERIAL PRIMARY KEY,
    first_name TEXT NOT NULL DEFAULT '',
    last_name  TEXT NOT NULL DEFAULT '',
    email      TEXT NOT NULL DEFAULT '',
    phone      TEXT NOT NULL DEFAULT '',
    address    TEXT NOT NULL DEFAULT '',
    notes      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS categories (
    id                   BIGSERIAL PRIMARY KEY,
    name                 TEXT NOT NULL UNIQUE,
    default_monthly_cost NUMERIC(12,2) NOT NULL DEFAULT 0,
    default_yearly_cost  NUMERIC(12,2) NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS vehicles (
    id             BIGSERIAL PRIMARY KEY,
    person_id      BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    category_id    BIGINT NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
    label          TEXT NOT NULL DEFAULT '',
    license_plate  TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    billing_period TEXT NOT NULL DEFAULT 'monthly'
                   CHECK (billing_period IN ('monthly', 'yearly')),
    cost_override  NUMERIC(12,2),
    start_date     DATE NOT NULL DEFAULT CURRENT_DATE,
    end_date       DATE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_vehicles_person ON vehicles(person_id);
CREATE INDEX IF NOT EXISTS idx_vehicles_category ON vehicles(category_id);

-- Seed a starting set of centrally-managed vehicle categories.
INSERT INTO categories (name, default_monthly_cost, default_yearly_cost) VALUES
    ('Auto',        50.00,  550.00),
    ('Anhänger',    30.00,  330.00),
    ('Wohnwagen',   70.00,  770.00),
    ('Wohnmobil',   90.00,  990.00),
    ('Motorrad',    25.00,  275.00)
ON CONFLICT (name) DO NOTHING;
