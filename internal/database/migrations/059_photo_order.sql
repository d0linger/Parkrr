-- Fotoreihenfolge + Titelbild (Hundert 58). Fotos hingen bisher stur an
-- created_at: das beste Bild (fürs Wiedererkennen im Planer und in der Galerie)
-- war das ZULETZT hochgeladene — meist der vierte Schnappschuss vom Kratzer,
-- nicht die Frontansicht.
--
-- sort_order ist die Anzeige-Reihenfolge, klein = vorn; Position 0 ist das
-- Titelbild (der Planer zeigt genau dieses). Backfill konserviert die heutige
-- sichtbare Reihenfolge (neueste zuerst), die Migration ändert also NICHTS am
-- aktuellen Anblick.
ALTER TABLE vehicle_photos ADD COLUMN IF NOT EXISTS sort_order INT NOT NULL DEFAULT 0;

WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY vehicle_id ORDER BY created_at DESC, id DESC) - 1 AS rn
    FROM vehicle_photos
)
UPDATE vehicle_photos vp SET sort_order = ranked.rn
FROM ranked WHERE ranked.id = vp.id AND vp.sort_order = 0 AND ranked.rn <> 0;

CREATE INDEX IF NOT EXISTS idx_vehicle_photos_order ON vehicle_photos (vehicle_id, sort_order);
