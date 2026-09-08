-- Datei-Anhänge je Person und Gefährt (Hundert 57): der Einstellvertrag, der
-- Typenschein-Scan, das Gutachten — bisher lagen sie "irgendwo" (Mail, Ordner,
-- Schreibtisch) und nie bei dem Datensatz, zu dem sie gehören.
--
-- Genau EIN Besitzer je Anhang (Person ODER Gefährt), per CHECK erzwungen.
-- ON DELETE CASCADE: ein Anhang ist Beiwerk seines Datensatzes, kein eigener
-- Beleg — aufbewahrungspflichtige Dokumente (Rechnungen, Protokolle) haben ihre
-- eigenen, unveränderlichen Tabellen.
CREATE TABLE IF NOT EXISTS attachments (
    id           BIGSERIAL PRIMARY KEY,
    person_id    BIGINT REFERENCES persons(id)  ON DELETE CASCADE,
    vehicle_id   BIGINT REFERENCES vehicles(id) ON DELETE CASCADE,
    filename     TEXT   NOT NULL DEFAULT '',
    content_type TEXT   NOT NULL,
    byte_size    INT    NOT NULL,
    data         BYTEA  NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((person_id IS NULL) <> (vehicle_id IS NULL))
);
CREATE INDEX IF NOT EXISTS idx_attachments_person  ON attachments (person_id)  WHERE person_id  IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_attachments_vehicle ON attachments (vehicle_id) WHERE vehicle_id IS NOT NULL;
