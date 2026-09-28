-- Complete tax-year ledger for Austrian income from letting and leasing.
-- The operational payment log stays immutable; payment_tax_metadata stores the
-- separately audited tax classification without weakening the payment guard.

CREATE TABLE IF NOT EXISTS tax_properties (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    address     TEXT NOT NULL DEFAULT '',
    postal_code TEXT NOT NULL DEFAULT '',
    eawz        TEXT NOT NULL DEFAULT '',
    is_default  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tax_properties_name ON tax_properties (lower(name));
CREATE UNIQUE INDEX IF NOT EXISTS uq_tax_properties_default ON tax_properties (is_default) WHERE is_default;

INSERT INTO tax_properties (name, is_default)
SELECT 'Standardobjekt', TRUE
WHERE NOT EXISTS (SELECT 1 FROM tax_properties);

ALTER TABLE garages ADD COLUMN IF NOT EXISTS tax_property_id BIGINT
    REFERENCES tax_properties(id) ON DELETE RESTRICT;
UPDATE garages
   SET tax_property_id = (SELECT id FROM tax_properties WHERE is_default ORDER BY id LIMIT 1)
 WHERE tax_property_id IS NULL;
CREATE INDEX IF NOT EXISTS idx_garages_tax_property ON garages(tax_property_id);

CREATE TABLE IF NOT EXISTS payment_tax_metadata (
    payment_id     BIGINT PRIMARY KEY REFERENCES payments(id) ON DELETE CASCADE,
    received_on    DATE,
    tax_property_id BIGINT NOT NULL REFERENCES tax_properties(id) ON DELETE RESTRICT,
    updated_by     BIGINT REFERENCES users(id) ON DELETE SET NULL,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_payment_tax_year
    ON payment_tax_metadata(received_on, tax_property_id);

CREATE TABLE IF NOT EXISTS tax_expense_categories (
    id         BIGSERIAL PRIMARY KEY,
    key        TEXT NOT NULL UNIQUE,
    label      TEXT NOT NULL,
    e1b_code   TEXT NOT NULL DEFAULT '9530',
    sort_order INT NOT NULL DEFAULT 0,
    active     BOOLEAN NOT NULL DEFAULT TRUE
);
INSERT INTO tax_expense_categories (key, label, e1b_code, sort_order) VALUES
    ('maintenance', 'Erhaltung / Instandsetzung', '9520', 10),
    ('financing', 'Fremdfinanzierung', '9510', 20),
    ('operating', 'Betriebskosten / Energie', '9530', 30),
    ('insurance', 'Versicherungen', '9530', 40),
    ('administration', 'Verwaltung / Steuerberatung', '9530', 50),
    ('travel', 'Fahrtkosten', '9530', 60),
    ('vat_payment', 'USt-Zahllast', '9530', 70),
    ('other', 'Sonstige Werbungskosten', '9530', 90)
ON CONFLICT (key) DO NOTHING;

CREATE TABLE IF NOT EXISTS tax_recurring_expenses (
    id              BIGSERIAL PRIMARY KEY,
    tax_property_id BIGINT NOT NULL REFERENCES tax_properties(id) ON DELETE RESTRICT,
    category_id     BIGINT NOT NULL REFERENCES tax_expense_categories(id) ON DELETE RESTRICT,
    description     TEXT NOT NULL,
    payee           TEXT NOT NULL DEFAULT '',
    amount          NUMERIC(12,2) NOT NULL CHECK (amount > 0),
    payment_method  TEXT NOT NULL DEFAULT 'ueberweisung',
    frequency       TEXT NOT NULL DEFAULT 'monthly' CHECK (frequency IN ('monthly','yearly')),
    due_day         INT NOT NULL DEFAULT 1 CHECK (due_day BETWEEN 1 AND 28),
    due_month       INT NOT NULL DEFAULT 1 CHECK (due_month BETWEEN 1 AND 12),
    start_on        DATE NOT NULL,
    end_on          DATE,
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_by      BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (end_on IS NULL OR end_on >= start_on)
);

CREATE TABLE IF NOT EXISTS tax_expenses (
    id                   BIGSERIAL PRIMARY KEY,
    tax_property_id      BIGINT NOT NULL REFERENCES tax_properties(id) ON DELETE RESTRICT,
    category_id          BIGINT NOT NULL REFERENCES tax_expense_categories(id) ON DELETE RESTRICT,
    recurring_expense_id BIGINT REFERENCES tax_recurring_expenses(id) ON DELETE SET NULL,
    paid_on              DATE NOT NULL,
    amount               NUMERIC(12,2) NOT NULL CHECK (amount <> 0),
    vat_amount           NUMERIC(12,2) NOT NULL DEFAULT 0,
    payee                TEXT NOT NULL DEFAULT '',
    description          TEXT NOT NULL,
    payment_method       TEXT NOT NULL DEFAULT 'ueberweisung',
    reverses_id          BIGINT UNIQUE REFERENCES tax_expenses(id) ON DELETE RESTRICT,
    created_by           BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((reverses_id IS NULL AND amount > 0 AND vat_amount >= 0)
        OR (reverses_id IS NOT NULL AND amount < 0 AND vat_amount <= 0))
);
CREATE INDEX IF NOT EXISTS idx_tax_expenses_year ON tax_expenses(paid_on, tax_property_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tax_recurring_booking
    ON tax_expenses(recurring_expense_id, paid_on) WHERE recurring_expense_id IS NOT NULL AND reverses_id IS NULL;

CREATE TABLE IF NOT EXISTS tax_expense_receipts (
    id           BIGSERIAL PRIMARY KEY,
    expense_id   BIGINT NOT NULL REFERENCES tax_expenses(id) ON DELETE RESTRICT,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL,
    byte_size    INT NOT NULL CHECK (byte_size > 0),
    sha256       TEXT NOT NULL,
    data         BYTEA NOT NULL,
    created_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tax_receipts_expense ON tax_expense_receipts(expense_id);

CREATE TABLE IF NOT EXISTS tax_assets (
    id                  BIGSERIAL PRIMARY KEY,
    tax_property_id     BIGINT NOT NULL REFERENCES tax_properties(id) ON DELETE RESTRICT,
    name                TEXT NOT NULL,
    in_service_on       DATE NOT NULL,
    depreciable_basis  NUMERIC(12,2) NOT NULL CHECK (depreciable_basis > 0),
    useful_life_years   NUMERIC(6,2) NOT NULL CHECK (useful_life_years > 0),
    half_year_rule      BOOLEAN NOT NULL DEFAULT TRUE,
    disposed_on         DATE,
    notes               TEXT NOT NULL DEFAULT '',
    created_by          BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (disposed_on IS NULL OR disposed_on >= in_service_on)
);

CREATE TABLE IF NOT EXISTS tax_year_locks (
    year       INT PRIMARY KEY CHECK (year BETWEEN 2000 AND 2100),
    locked_by  BIGINT REFERENCES users(id) ON DELETE SET NULL,
    locked_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tax_settings (
    singleton            BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    small_business_limit NUMERIC(12,2) NOT NULL DEFAULT 55000 CHECK (small_business_limit > 0),
    warning_percent      NUMERIC(5,2) NOT NULL DEFAULT 90 CHECK (warning_percent > 0 AND warning_percent <= 100),
    vat_opted_in         BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO tax_settings(singleton) VALUES (TRUE) ON CONFLICT (singleton) DO NOTHING;

-- Expense rows and receipt bytes are append-only. A correction is a new negative
-- expense whose reverses_id points to the original, followed by a fresh booking.
CREATE OR REPLACE FUNCTION parkrr_guard_tax_ledger() RETURNS trigger AS $$
BEGIN
    IF parkrr_purge_allowed() THEN
        RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    END IF;
    RAISE EXCEPTION '% rows are immutable: create a reversal instead', TG_TABLE_NAME
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_guard_tax_expenses ON tax_expenses;
CREATE TRIGGER trg_guard_tax_expenses BEFORE UPDATE OR DELETE ON tax_expenses
    FOR EACH ROW EXECUTE FUNCTION parkrr_guard_tax_ledger();
DROP TRIGGER IF EXISTS trg_guard_tax_receipts ON tax_expense_receipts;
CREATE TRIGGER trg_guard_tax_receipts BEFORE UPDATE OR DELETE ON tax_expense_receipts
    FOR EACH ROW EXECUTE FUNCTION parkrr_guard_tax_ledger();
