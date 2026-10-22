-- Mail-Warteschlange (Ausbau 36) für automatische Mails: Rechnungsversand (19) und
-- automatische Mahnstufen (20). Manuelle Erinnerungen bleiben synchron.
CREATE TABLE IF NOT EXISTS mail_outbox (
    id              BIGSERIAL PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('invoice', 'reminder')),
    ref_id          BIGINT,                       -- Rechnung
    person_id       BIGINT REFERENCES persons(id) ON DELETE SET NULL,
    level           INT NOT NULL DEFAULT 0,       -- Mahnstufe bei kind = 'reminder'
    recipient       TEXT NOT NULL,
    subject         TEXT NOT NULL,
    body            TEXT NOT NULL,
    attachment_name TEXT NOT NULL DEFAULT '',
    attachment      BYTEA,                        -- nach dem Versand geleert
    status          TEXT NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'sending', 'sent', 'failed')),
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at      TIMESTAMPTZ,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_mail_outbox_due ON mail_outbox (next_attempt_at) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS idx_mail_outbox_ref ON mail_outbox (kind, ref_id);
CREATE INDEX IF NOT EXISTS idx_mail_outbox_person ON mail_outbox (person_id);
-- Je Rechnung höchstens eine Mahnung unterwegs: keine doppelte Stufe aus der Automatik.
CREATE UNIQUE INDEX IF NOT EXISTS uq_mail_outbox_pending_reminder
    ON mail_outbox (ref_id) WHERE kind = 'reminder' AND status IN ('queued', 'sending');

-- Mail-Automatik, getrennt von den Rechnungsdaten (dort hängt der Nummernkreis).
-- Alles standardmäßig AUS.
CREATE TABLE IF NOT EXISTS mail_automation (
    id                 INT PRIMARY KEY CHECK (id = 1),
    auto_send_invoices BOOLEAN NOT NULL DEFAULT FALSE,
    auto_dunning       BOOLEAN NOT NULL DEFAULT FALSE,
    dunning_days       INT NOT NULL DEFAULT 14 CHECK (dunning_days BETWEEN 3 AND 90),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO mail_automation (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- Je Person abschaltbar: nie automatisch mahnen (manuell bleibt möglich).
ALTER TABLE persons ADD COLUMN IF NOT EXISTS no_dunning BOOLEAN NOT NULL DEFAULT FALSE;
