-- Kundenwünsche aus dem Portal (Hundert 85 + 87): Stammdaten-Änderung und
-- Abholtermin. Das Portal war bisher strikt lesend — jede Änderung lief über
-- Telefon oder Zuruf und ging verloren. Ein WUNSCH ist bewusst KEINE Änderung:
-- der Kunde schreibt nie direkt in die Stammdaten, der Betreiber übernimmt (oder
-- eben nicht) — mit Protokoll.
CREATE TABLE IF NOT EXISTS portal_requests (
    id          BIGSERIAL PRIMARY KEY,
    person_id   BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    kind        TEXT   NOT NULL CHECK (kind IN ('contact_update', 'pickup')),
    payload     JSONB  NOT NULL DEFAULT '{}'::jsonb,
    status      TEXT   NOT NULL DEFAULT 'offen' CHECK (status IN ('offen', 'erledigt', 'abgelehnt')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    resolved_by BIGINT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_portal_requests_open ON portal_requests (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_portal_requests_person ON portal_requests (person_id, created_at DESC);
