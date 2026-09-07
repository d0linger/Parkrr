-- Belegungshistorie je Stellplatz (Hundert 79). "Wer stand im März auf Platz 3?"
-- war bisher unbeantwortbar: vehicles.spot_id ist eine nackte Spalte, jede
-- Umplatzierung überschreibt die vorige spurlos.
--
-- Ein TRIGGER statt Handler-Code, mit Bedacht: spot_id ändert sich auf VIELEN
-- Wegen (zuweisen, entfernen, Platz löschen → SET NULL, Gefährt löschen,
-- Auto-Anordnen legt Plätze an und um). Ein Trigger sieht sie alle — ein
-- vergessener Handler-Pfad ist genau das Loch, das eine Historie wertlos macht.
--
-- Labels werden im MOMENT des Ereignisses eingefroren (Platz- und Gefährt-Name):
-- die Historie soll lesbar bleiben, wenn Platz oder Gefährt längst weg sind.
-- Deshalb auch KEINE Fremdschlüssel mit CASCADE — die Geschichte überlebt beide.
CREATE TABLE IF NOT EXISTS spot_occupancy_history (
    id            BIGSERIAL PRIMARY KEY,
    spot_id       BIGINT NOT NULL,
    spot_label    TEXT   NOT NULL DEFAULT '',
    hall_id       BIGINT,
    vehicle_id    BIGINT NOT NULL,
    vehicle_label TEXT   NOT NULL DEFAULT '',
    person_id     BIGINT,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_spot_hist_spot ON spot_occupancy_history (spot_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_spot_hist_vehicle ON spot_occupancy_history (vehicle_id, started_at DESC);
-- Höchstens EIN offener Eintrag je Gefährt: der Trigger schließt vor jedem Öffnen.
CREATE UNIQUE INDEX IF NOT EXISTS uq_spot_hist_open ON spot_occupancy_history (vehicle_id) WHERE ended_at IS NULL;

CREATE OR REPLACE FUNCTION parkrr_track_spot_occupancy() RETURNS trigger AS $$
DECLARE
    v_label TEXT;
    s_label TEXT;
    s_hall  BIGINT;
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE spot_occupancy_history SET ended_at = now()
         WHERE vehicle_id = OLD.id AND ended_at IS NULL;
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' AND NEW.spot_id IS NOT DISTINCT FROM OLD.spot_id THEN
        RETURN NEW; -- kein Platzwechsel, nichts zu historisieren
    END IF;
    IF TG_OP = 'UPDATE' THEN
        UPDATE spot_occupancy_history SET ended_at = now()
         WHERE vehicle_id = NEW.id AND ended_at IS NULL;
    END IF;
    IF NEW.spot_id IS NOT NULL THEN
        SELECT COALESCE(NULLIF(NEW.label,''), NULLIF(NEW.license_plate,''), 'Gefährt') INTO v_label;
        SELECT s.label, s.hall_id INTO s_label, s_hall FROM spots s WHERE s.id = NEW.spot_id;
        INSERT INTO spot_occupancy_history (spot_id, spot_label, hall_id, vehicle_id, vehicle_label, person_id)
        VALUES (NEW.spot_id, COALESCE(s_label, ''), s_hall, NEW.id, v_label, NEW.person_id);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_track_spot_occupancy ON vehicles;
CREATE TRIGGER trg_track_spot_occupancy
    AFTER INSERT OR UPDATE OF spot_id OR DELETE ON vehicles
    FOR EACH ROW EXECUTE FUNCTION parkrr_track_spot_occupancy();
