-- Standardmaße je Tarif/Kategorie (Hundert 80). Ein ungemessenes Gefährt hatte im
-- Garagenplaner GAR KEINE Maße: keine Passform-Warnung, kein maßstäbliches
-- Rechteck, nur der Knopf "Maße festlegen". Dabei ist die Kategorie ("Wohnwagen",
-- "Motorrad") fast immer eine brauchbare erste Näherung.
--
-- NULL = keine Vorgabe (Default): Bestandsdaten und Anzeige bleiben unverändert,
-- bis ein Betreiber die Vorgabe je Kategorie pflegt. Eigene Maße am Gefährt
-- gewinnen IMMER — die Vorgabe greift nur, wo nichts gemessen ist (COALESCE in
-- den Planer-Abfragen).
ALTER TABLE categories ADD COLUMN IF NOT EXISTS default_length_m NUMERIC(6,2);
ALTER TABLE categories ADD COLUMN IF NOT EXISTS default_width_m  NUMERIC(6,2);
ALTER TABLE categories ADD COLUMN IF NOT EXISTS default_height_m NUMERIC(6,2);
ALTER TABLE categories ADD COLUMN IF NOT EXISTS default_weight_t NUMERIC(7,3);

-- Dieselben Obergrenzen, die der Maße-Endpunkt für Gefährte durchsetzt (dimPos):
-- eine Vorgabe jenseits davon wäre über die API gar nicht erfassbar und nur per
-- Direktschreibzugriff zu erzeugen — der CHECK hält beide Wege konsistent.
ALTER TABLE categories DROP CONSTRAINT IF EXISTS categories_default_dims_range;
ALTER TABLE categories ADD CONSTRAINT categories_default_dims_range CHECK (
    (default_length_m IS NULL OR (default_length_m > 0 AND default_length_m <= 60)) AND
    (default_width_m  IS NULL OR (default_width_m  > 0 AND default_width_m  <= 15)) AND
    (default_height_m IS NULL OR (default_height_m > 0 AND default_height_m <= 15)) AND
    (default_weight_t IS NULL OR (default_weight_t > 0 AND default_weight_t <= 200))
);
