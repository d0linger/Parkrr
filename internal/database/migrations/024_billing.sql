-- Invoicing (Austria). A GUI-editable billing-settings singleton (seller data +
-- tax treatment) and immutable, sequentially numbered invoices with a snapshot
-- of seller/buyer/tax at issue time, so a later settings change never mutates an
-- already-issued invoice.

CREATE TABLE IF NOT EXISTS billing_settings (
    id                 INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    seller_name        TEXT NOT NULL DEFAULT '',
    seller_address     TEXT NOT NULL DEFAULT '',
    seller_uid         TEXT NOT NULL DEFAULT '',   -- ATU / UID-Nummer (Pflicht bei USt-Ausweis)
    kleinunternehmer   BOOLEAN NOT NULL DEFAULT TRUE, -- § 6 Abs 1 Z 27 UStG: keine USt
    ust_rate           NUMERIC(5,2) NOT NULL DEFAULT 20.00, -- 20 / 13 / 10; ignoriert wenn Kleinunternehmer
    invoice_prefix     TEXT NOT NULL DEFAULT '',    -- z. B. "2026-"
    next_invoice_no    INT NOT NULL DEFAULT 1,
    number_pad         INT NOT NULL DEFAULT 4,      -- Nullen-Auffüllung der laufenden Nummer
    iban               TEXT NOT NULL DEFAULT '',
    bic                TEXT NOT NULL DEFAULT '',
    payment_terms_days INT NOT NULL DEFAULT 14,
    footer_note        TEXT NOT NULL DEFAULT ''
);
INSERT INTO billing_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS invoices (
    id               BIGSERIAL PRIMARY KEY,
    number           TEXT NOT NULL UNIQUE,          -- fortlaufende Rechnungsnummer
    person_id        BIGINT NOT NULL REFERENCES persons(id) ON DELETE RESTRICT,
    issued_on        DATE NOT NULL DEFAULT CURRENT_DATE,
    due_on           DATE,
    subtotal         NUMERIC(12,2) NOT NULL DEFAULT 0, -- Netto (bei USt) bzw. Gesamt (Kleinunternehmer)
    ust_rate         NUMERIC(5,2) NOT NULL DEFAULT 0,
    tax_amount       NUMERIC(12,2) NOT NULL DEFAULT 0,
    total            NUMERIC(12,2) NOT NULL DEFAULT 0,
    kleinunternehmer BOOLEAN NOT NULL DEFAULT TRUE,
    seller_snapshot  JSONB NOT NULL DEFAULT '{}',
    buyer_snapshot   JSONB NOT NULL DEFAULT '{}',
    note             TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by       BIGINT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_invoices_person ON invoices(person_id, issued_on DESC);

CREATE TABLE IF NOT EXISTS invoice_items (
    id          BIGSERIAL PRIMARY KEY,
    invoice_id  BIGINT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    pos         INT NOT NULL,
    description TEXT NOT NULL,
    quantity    NUMERIC(10,2) NOT NULL DEFAULT 1,
    unit_amount NUMERIC(12,2) NOT NULL,            -- je Einheit (Netto bei USt, sonst Gesamt)
    line_total  NUMERIC(12,2) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_invoice_items_invoice ON invoice_items(invoice_id);
