-- Feature expansion: roles, 2FA, session metadata, vehicle status/reservations,
-- status history, photos, payments, extra charges, service catalog, audit log.

-- ---------- Users: roles + 2FA ----------
ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'manager'
    CHECK (role IN ('admin', 'manager', 'accounting', 'readonly'));
UPDATE users SET role = 'admin' WHERE is_admin = TRUE AND role <> 'admin';

ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret  TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- ---------- Sessions: metadata for session management ----------
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS ip         TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_seen  TIMESTAMPTZ NOT NULL DEFAULT now();

-- ---------- Vehicles: lifecycle status + reservation window ----------
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'stored'
    CHECK (status IN ('reserved', 'stored', 'collected', 'cancelled'));
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS reserved_from  DATE;
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS reserved_until DATE;
UPDATE vehicles SET status = 'collected' WHERE end_date IS NOT NULL AND status = 'stored';

CREATE TABLE IF NOT EXISTS vehicle_status_history (
    id         BIGSERIAL PRIMARY KEY,
    vehicle_id BIGINT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    old_status TEXT NOT NULL DEFAULT '',
    new_status TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    changed_by TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_vsh_vehicle ON vehicle_status_history(vehicle_id);

-- ---------- Vehicle photos (stored in DB, self-contained) ----------
CREATE TABLE IF NOT EXISTS vehicle_photos (
    id           BIGSERIAL PRIMARY KEY,
    vehicle_id   BIGINT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL,
    byte_size    INT NOT NULL DEFAULT 0,
    data         BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_photos_vehicle ON vehicle_photos(vehicle_id);

-- ---------- Payments (open items are derived) ----------
CREATE TABLE IF NOT EXISTS payments (
    id         BIGSERIAL PRIMARY KEY,
    person_id  BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    vehicle_id BIGINT REFERENCES vehicles(id) ON DELETE SET NULL,
    amount     NUMERIC(12,2) NOT NULL,
    paid_on    DATE NOT NULL DEFAULT CURRENT_DATE,
    method     TEXT NOT NULL DEFAULT '',
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_payments_person ON payments(person_id);

-- ---------- Service catalog + extra charges ----------
CREATE TABLE IF NOT EXISTS service_types (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    default_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO service_types (name, default_amount) VALUES
    ('Strom',        15.00),
    ('Reinigung',    40.00),
    ('Winterservice', 60.00),
    ('Reifenwechsel', 35.00)
ON CONFLICT (name) DO NOTHING;

CREATE TABLE IF NOT EXISTS charges (
    id          BIGSERIAL PRIMARY KEY,
    person_id   BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    vehicle_id  BIGINT REFERENCES vehicles(id) ON DELETE SET NULL,
    description TEXT NOT NULL,
    amount      NUMERIC(12,2) NOT NULL,
    quantity    NUMERIC(10,2) NOT NULL DEFAULT 1,
    charged_on  DATE NOT NULL DEFAULT CURRENT_DATE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_charges_person ON charges(person_id);

-- ---------- Audit log ----------
CREATE TABLE IF NOT EXISTS audit_log (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT,
    username   TEXT NOT NULL DEFAULT '',
    action     TEXT NOT NULL,
    entity     TEXT NOT NULL,
    entity_id  BIGINT,
    summary    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at DESC);
