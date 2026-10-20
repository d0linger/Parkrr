-- Ausbau Paket 5: Saison und Kunden.

-- Abholung in Zeitfenstern (Ausbau 23): wöchentliche Fenster, in denen Kunden im
-- Portal einen Abholtermin wählen können. Ohne Fenster bleibt es beim Wunschdatum.
CREATE TABLE IF NOT EXISTS pickup_windows (
    id         BIGSERIAL PRIMARY KEY,
    weekday    SMALLINT NOT NULL CHECK (weekday BETWEEN 1 AND 7),      -- ISO: 1 = Montag
    start_min  INT NOT NULL CHECK (start_min BETWEEN 0 AND 1439),      -- Minuten ab Mitternacht
    end_min    INT NOT NULL CHECK (end_min BETWEEN 1 AND 1440),
    slot_min   INT NOT NULL DEFAULT 30 CHECK (slot_min BETWEEN 10 AND 240),
    capacity   INT NOT NULL DEFAULT 1 CHECK (capacity BETWEEN 1 AND 20),
    CHECK (end_min > start_min)
);

-- Warteliste (Ausbau 31). Einträge sind Interessenten, noch keine Personen; sie
-- werden nach 12 Monaten gelöscht (Wartung), weil die Einwilligung nur dafür gilt.
CREATE TABLE IF NOT EXISTS waitlist (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    email        TEXT NOT NULL DEFAULT '',
    phone        TEXT NOT NULL DEFAULT '',
    category_id  BIGINT REFERENCES categories(id) ON DELETE SET NULL,
    length_m     NUMERIC(6,2) CHECK (length_m IS NULL OR (length_m > 0 AND length_m <= 60)),
    width_m      NUMERIC(6,2) CHECK (width_m IS NULL OR (width_m > 0 AND width_m <= 15)),
    wanted_from  DATE,
    wanted_until DATE,
    note         TEXT NOT NULL DEFAULT '',
    consent      BOOLEAN NOT NULL DEFAULT FALSE,
    status       TEXT NOT NULL DEFAULT 'offen' CHECK (status IN ('offen', 'uebernommen', 'abgesagt')),
    person_id    BIGINT REFERENCES persons(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_waitlist_open ON waitlist (status, created_at);

-- Kunden vor Vertragsende erinnern (Ausbau 22): je Gefährt und Vertragsende genau
-- eine Erinnerung, auch wenn der Lauf mehrmals am Tag prüft.
CREATE TABLE IF NOT EXISTS contract_end_notices (
    vehicle_id BIGINT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    end_date   DATE NOT NULL,
    queued_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (vehicle_id, end_date)
);
ALTER TABLE mail_automation ADD COLUMN IF NOT EXISTS end_reminder_days INT NOT NULL DEFAULT 0
    CHECK (end_reminder_days BETWEEN 0 AND 60); -- 0 = aus

-- Neue Arten: Verlängerungswunsch aus dem Portal, Vertragsende-Erinnerung per Mail.
ALTER TABLE portal_requests DROP CONSTRAINT IF EXISTS portal_requests_kind_check;
ALTER TABLE portal_requests ADD CONSTRAINT portal_requests_kind_check
    CHECK (kind IN ('contact_update', 'pickup', 'extend'));
ALTER TABLE mail_outbox DROP CONSTRAINT IF EXISTS mail_outbox_kind_check;
ALTER TABLE mail_outbox ADD CONSTRAINT mail_outbox_kind_check
    CHECK (kind IN ('invoice', 'reminder', 'contract_end'));
