-- Ausbau B6 (Halle): Zustandsfotos an der Übergabe, Serviceaufgaben, Schlüsselbuch.

-- ---------- 24 Zustandsfotos an der Übergabe ----------
-- Ein Foto gehört entweder zur Galerie des Gefährts (handover_id NULL) oder zu genau
-- einem Übergabeprotokoll. Protokollfotos sind Teil des Belegs: sie verschwinden nur
-- mit dem Protokoll (Löschen ist admin-only, 051/068), nie einzeln.
ALTER TABLE vehicle_photos ADD COLUMN IF NOT EXISTS handover_id BIGINT
    REFERENCES handover_protocols(id) ON DELETE CASCADE;
-- Feste Ansicht, damit Einlagerung und Abholung Bild für Bild vergleichbar sind.
ALTER TABLE vehicle_photos ADD COLUMN IF NOT EXISTS view TEXT NOT NULL DEFAULT '';
ALTER TABLE vehicle_photos DROP CONSTRAINT IF EXISTS vehicle_photos_view_check;
ALTER TABLE vehicle_photos ADD CONSTRAINT vehicle_photos_view_check
    CHECK (view IN ('', 'vorne', 'hinten', 'links', 'rechts', 'innen', 'km', 'schaden'));
CREATE INDEX IF NOT EXISTS idx_vehicle_photos_handover
    ON vehicle_photos (handover_id, id) WHERE handover_id IS NOT NULL;

-- Schäden als Liste ({"area": "...", "note": "..."}) statt Freitext. Die Spalte fällt
-- unter den Unveränderlichkeits-Trigger aus 051 (er vergleicht die ganze Zeile).
ALTER TABLE handover_protocols ADD COLUMN IF NOT EXISTS damages JSONB NOT NULL DEFAULT '[]'::jsonb;

-- ---------- 25 Serviceaufgaben während der Einlagerung ----------
-- scope: 'vehicle' (ein Gefährt), 'category' (alle eines Tarifs), 'all' (alle
-- eingelagerten), 'power' (alle eingelagerten mit Ladebedarf). Fällig ist eine
-- Aufgabe je Gefährt: letzte Erledigung (oder Einlagerungsbeginn bzw. Anlage der
-- Aufgabe, der spätere Tag) plus Intervall.
CREATE TABLE IF NOT EXISTS service_tasks (
    id              BIGSERIAL PRIMARY KEY,
    title           TEXT NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 120),
    scope           TEXT NOT NULL CHECK (scope IN ('vehicle', 'category', 'all', 'power')),
    vehicle_id      BIGINT REFERENCES vehicles(id) ON DELETE CASCADE,
    category_id     BIGINT REFERENCES categories(id) ON DELETE CASCADE,
    interval_days   INT NOT NULL CHECK (interval_days BETWEEN 1 AND 730),
    service_type_id BIGINT REFERENCES service_types(id) ON DELETE SET NULL,
    note            TEXT NOT NULL DEFAULT '',
    active          BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope = 'vehicle') = (vehicle_id IS NOT NULL)),
    CHECK ((scope = 'category') = (category_id IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS service_task_log (
    id         BIGSERIAL PRIMARY KEY,
    task_id    BIGINT NOT NULL REFERENCES service_tasks(id) ON DELETE CASCADE,
    vehicle_id BIGINT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    done_on    DATE NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    charge_id  BIGINT REFERENCES charges(id) ON DELETE SET NULL,
    done_by    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Ein Doppeltipp (oder ein wiederholter Request) erledigt nicht zweimal.
    UNIQUE (task_id, vehicle_id, done_on)
);
CREATE INDEX IF NOT EXISTS idx_service_task_log_last
    ON service_task_log (task_id, vehicle_id, done_on DESC);

-- ---------- 34 Schlüssel- und Handsenderbuch ----------
-- Nur das Zugangsmittel selbst (Art und Nummer), nie ein Code im Klartext.
CREATE TABLE IF NOT EXISTS access_media (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('schluessel', 'handsender', 'karte', 'code')),
    label      TEXT NOT NULL CHECK (length(btrim(label)) BETWEEN 1 AND 60),
    hall_id    BIGINT REFERENCES halls(id) ON DELETE SET NULL,
    note       TEXT NOT NULL DEFAULT '',
    retired    BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, label)
);

CREATE TABLE IF NOT EXISTS access_media_loans (
    id          BIGSERIAL PRIMARY KEY,
    medium_id   BIGINT NOT NULL REFERENCES access_media(id) ON DELETE CASCADE,
    person_id   BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    issued_on   DATE NOT NULL,
    returned_on DATE,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (returned_on IS NULL OR returned_on >= issued_on)
);
-- Ein Zugangsmittel ist zu jeder Zeit höchstens einmal ausgegeben.
CREATE UNIQUE INDEX IF NOT EXISTS uq_access_media_open_loan
    ON access_media_loans (medium_id) WHERE returned_on IS NULL;
CREATE INDEX IF NOT EXISTS idx_access_media_loans_person
    ON access_media_loans (person_id) WHERE returned_on IS NULL;
