-- Ausbau B7 (Geld und Recht): Kautionen, Kontoauszug-Import.

-- ---------- 32 Kautionen getrennt führen ----------
-- Eine Kaution ist kein Erlös, solange sie nicht einbehalten wird: sie steht in einem
-- eigenen Konto je Person, nicht bei den Zahlungen. Einbehalten bucht den Betrag über
-- den normalen Zahlungsweg (payment_id), erst dann zählt er als Erlös.
-- Buchungen sind Grundaufzeichnungen: die Person lässt sich mit Kautionsbuchungen
-- nicht löschen (RESTRICT, wie Rechnungen); dafür gibt es die Anonymisierung.
CREATE TABLE IF NOT EXISTS deposits (
    id         BIGSERIAL PRIMARY KEY,
    person_id  BIGINT NOT NULL REFERENCES persons(id) ON DELETE RESTRICT,
    vehicle_id BIGINT REFERENCES vehicles(id) ON DELETE SET NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('erhalten', 'einbehalten', 'zurueck')),
    amount     NUMERIC(12,2) NOT NULL CHECK (amount > 0),
    booked_on  DATE NOT NULL,
    method     TEXT NOT NULL DEFAULT '',
    note       TEXT NOT NULL DEFAULT '',
    payment_id BIGINT REFERENCES payments(id) ON DELETE RESTRICT,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_deposits_person ON deposits (person_id, booked_on, id);

-- ---------- 27 Kontoauszug importieren (camt.053) ----------
-- Eingelesene Gutschriften. ref ist die Buchungsreferenz der Bank (oder ein Hash der
-- Buchung, wenn die Bank keine mitliefert): derselbe Auszug zweimal importiert legt
-- nichts doppelt an. Zugeordnet wird nur per Klick, über den normalen Zahlungsweg.
CREATE TABLE IF NOT EXISTS bank_transactions (
    id          BIGSERIAL PRIMARY KEY,
    ref         TEXT NOT NULL UNIQUE,
    booked_on   DATE NOT NULL,
    amount      NUMERIC(12,2) NOT NULL CHECK (amount > 0),
    currency    TEXT NOT NULL DEFAULT 'EUR',
    name        TEXT NOT NULL DEFAULT '',
    iban        TEXT NOT NULL DEFAULT '',
    remittance  TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'offen' CHECK (status IN ('offen', 'zugeordnet', 'ignoriert')),
    payment_id  BIGINT REFERENCES payments(id) ON DELETE SET NULL,
    person_id   BIGINT REFERENCES persons(id) ON DELETE SET NULL,
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    resolved_by BIGINT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_bank_transactions_open ON bank_transactions (booked_on) WHERE status = 'offen';

-- ---------- 30 Einstellvertrag ----------
-- Jede gespeicherte Fassung ist eine eigene Version; eine Annahme hängt an genau ihrer
-- Version. Das angenommene PDF liegt als Anhang bei der Person.
CREATE TABLE IF NOT EXISTS contract_templates (
    id         BIGSERIAL PRIMARY KEY,
    version    INT NOT NULL UNIQUE CHECK (version > 0),
    title      TEXT NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 120),
    body       TEXT NOT NULL,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS contract_acceptances (
    id            BIGSERIAL PRIMARY KEY,
    person_id     BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    vehicle_id    BIGINT REFERENCES vehicles(id) ON DELETE SET NULL,
    template_id   BIGINT NOT NULL REFERENCES contract_templates(id) ON DELETE RESTRICT,
    accepted_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    via           TEXT NOT NULL CHECK (via IN ('portal', 'betreiber')),
    attachment_id BIGINT REFERENCES attachments(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_contract_acceptances_person ON contract_acceptances (person_id, accepted_at DESC);

-- ---------- 29 Wertsicherung nach Verbraucherpreisindex ----------
-- Die Indexwerte trägt der Betreiber ein (kein Abruf von außen). Je Tarif optional ein
-- Basismonat und ein Schwellwert in Prozent.
CREATE TABLE IF NOT EXISTS cpi_values (
    month      DATE PRIMARY KEY CHECK (extract(day FROM month) = 1),
    value      NUMERIC(8,2) NOT NULL CHECK (value > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE categories ADD COLUMN IF NOT EXISTS index_base_month DATE;
ALTER TABLE categories ADD COLUMN IF NOT EXISTS index_threshold NUMERIC(5,2) NOT NULL DEFAULT 5;
ALTER TABLE categories DROP CONSTRAINT IF EXISTS categories_index_threshold_check;
ALTER TABLE categories ADD CONSTRAINT categories_index_threshold_check CHECK (index_threshold >= 0 AND index_threshold <= 50);

-- Preisänderung je Gefährt ab einem Periodenbeginn. vehicles.rate bleibt der
-- ursprünglich vereinbarte Satz; die Abrechnung teilt jede Periode an diesen Daten
-- (models.FlatRatePeriod.sliceCents). index_month ist die neue Basis für die nächste
-- Anpassung.
CREATE TABLE IF NOT EXISTS vehicle_rate_changes (
    id             BIGSERIAL PRIMARY KEY,
    vehicle_id     BIGINT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    effective_from DATE NOT NULL,
    rate           NUMERIC(12,2) NOT NULL CHECK (rate >= 0),
    previous_rate  NUMERIC(12,2) NOT NULL,
    index_month    DATE,
    reason         TEXT NOT NULL DEFAULT '',
    created_by     BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (vehicle_id, effective_from)
);

ALTER TABLE mail_outbox DROP CONSTRAINT IF EXISTS mail_outbox_kind_check;
ALTER TABLE mail_outbox ADD CONSTRAINT mail_outbox_kind_check
    CHECK (kind IN ('invoice', 'reminder', 'contract_end', 'price_change'));
