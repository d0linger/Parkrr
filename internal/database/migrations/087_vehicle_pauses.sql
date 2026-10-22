-- Ruhezeit („Kurz weg“): ein eingelagertes Gefährt ist ein paar Wochen unterwegs
-- (Urlaub, Messe) und kommt zurück. Entscheidungen vom 16.10.2026:
--   Platz je Ruhezeit halten oder freigeben (Standard halten), keine Haltegebühr,
--   gekürzt ab dem ersten Tag, unter einer Pauschale nur vermerkt, vorab bezahlt
--   verschiebt "bezahlt bis", gebundene Nebenkosten ruhen mit, Kunden fragen im Portal an.
--
-- Abrechnung: von start_date bis einschließlich until ruht die Miete. Solange niemand
-- „Ist zurück“ getippt hat (ended = false), ruht sie weiter, auch über das geplante
-- until hinaus — nichts springt von selbst auf volle Miete zurück.
CREATE TABLE IF NOT EXISTS vehicle_pauses (
    id                  BIGSERIAL PRIMARY KEY,
    vehicle_id          BIGINT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    start_date          DATE NOT NULL,
    until               DATE,
    ended               BOOLEAN NOT NULL DEFAULT false,
    hold_spot           BOOLEAN NOT NULL DEFAULT true,
    prev_spot_id        BIGINT REFERENCES spots(id) ON DELETE SET NULL,
    paid_through_before DATE,
    note                TEXT NOT NULL DEFAULT '',
    source              TEXT NOT NULL DEFAULT 'betreiber' CHECK (source IN ('betreiber', 'portal')),
    created_by          BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (until IS NULL OR until >= start_date),
    CHECK (NOT ended OR until IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_vehicle_pauses_vehicle ON vehicle_pauses (vehicle_id, start_date);

ALTER TABLE portal_requests DROP CONSTRAINT IF EXISTS portal_requests_kind_check;
ALTER TABLE portal_requests ADD CONSTRAINT portal_requests_kind_check
    CHECK (kind IN ('contact_update', 'pickup', 'extend', 'pause'));
