-- Halter-Snapshot für Übergabeprotokolle und Gefährt-Anhänge (Audit PRT-01).
--
-- Bisher kannten beide Tabellen nur das GEFÄHRT. Wem ein Protokoll oder ein
-- Anhang gehört, wurde bei jeder Abfrage über vehicles.person_id aufgelöst — also
-- über den HEUTIGEN Halter. Wechselte ein Gefährt die Person, sah der neue Halter
-- im Portal die unterschriebenen Protokolle des alten, das PDF druckte ihn als
-- "Halter/in" auf einen fremden Beleg, und die Anonymisierung (Art. 17) traf die
-- falsche Person: die Unterschrift des alten Halters blieb stehen, die des neuen
-- Kunden wurde von einem fremden Beleg gewischt.
--
-- Jetzt wird der Halter beim ANLEGEN festgeschrieben:
--   handover_protocols.person_id    — wer das Gefährt bei der Übergabe hielt
--   attachments.owner_person_id     — die Person, die der Anhang betrifft
--                                     (bei Personen-Anhängen = person_id, bei
--                                     Gefährt-Anhängen der Halter beim Hochladen)
-- attachments.person_id bleibt unverändert der EXKLUSIVE Besitzer aus 063 (CHECK
-- genau ein Besitzer); die neue Spalte ist davon unabhängig.
--
-- Backfill: bestehende Zeilen bekommen den AKTUELLEN Halter des Gefährts — eine
-- frühere Zuordnung ist nirgends gespeichert und lässt sich nicht rekonstruieren.
--
-- Übergabeprotokolle sind seit 051 unveränderlich (jede neue Spalte automatisch
-- mit). Der einmalige Backfill läuft deshalb mit parkrr.purge, per SET LOCAL auf
-- die Migrations-Transaktion begrenzt und direkt danach wieder abgeschaltet.
-- Danach ist person_id so unveränderlich wie der Rest des Belegs.

ALTER TABLE handover_protocols ADD COLUMN IF NOT EXISTS person_id BIGINT;
ALTER TABLE attachments        ADD COLUMN IF NOT EXISTS owner_person_id BIGINT;

SET LOCAL parkrr.purge = 'on';
UPDATE handover_protocols hp
   SET person_id = v.person_id
  FROM vehicles v
 WHERE v.id = hp.vehicle_id
   AND hp.person_id IS NULL;
SET LOCAL parkrr.purge = 'off';

UPDATE attachments a
   SET owner_person_id = COALESCE(a.person_id,
                                  (SELECT v.person_id FROM vehicles v WHERE v.id = a.vehicle_id))
 WHERE a.owner_person_id IS NULL;

ALTER TABLE handover_protocols ALTER COLUMN person_id SET NOT NULL;
ALTER TABLE attachments        ALTER COLUMN owner_person_id SET NOT NULL;

-- RESTRICT wie beim Gefährt (068): ein unterschriebener Beleg verschwindet nicht
-- still mit der Person; für die Löschung gibt es die Anonymisierung.
ALTER TABLE handover_protocols
    ADD CONSTRAINT handover_protocols_person_id_fkey
        FOREIGN KEY (person_id) REFERENCES persons(id) ON DELETE RESTRICT;
-- CASCADE wie die Besitzerspalten aus 063: ein Anhang ist Beiwerk, kein Beleg.
ALTER TABLE attachments
    ADD CONSTRAINT attachments_owner_person_id_fkey
        FOREIGN KEY (owner_person_id) REFERENCES persons(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_handover_person
    ON handover_protocols(person_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_attachments_owner_person
    ON attachments(owner_person_id);

-- Tiefenverteidigung: ein INSERT, der den Halter nicht mitgibt (direkt in der DB,
-- ein künftiger Code-Pfad), bekommt den aktuellen Halter des Gefährts, statt an
-- NOT NULL zu scheitern oder — schlimmer — einen falschen Wert zu erfinden.
CREATE OR REPLACE FUNCTION parkrr_handover_owner_snapshot() RETURNS trigger AS $$
BEGIN
    IF NEW.person_id IS NULL THEN
        SELECT v.person_id INTO NEW.person_id FROM vehicles v WHERE v.id = NEW.vehicle_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_handover_owner_snapshot ON handover_protocols;
CREATE TRIGGER trg_handover_owner_snapshot
    BEFORE INSERT ON handover_protocols
    FOR EACH ROW EXECUTE FUNCTION parkrr_handover_owner_snapshot();

CREATE OR REPLACE FUNCTION parkrr_attachment_owner_snapshot() RETURNS trigger AS $$
BEGIN
    IF NEW.owner_person_id IS NULL THEN
        NEW.owner_person_id := COALESCE(NEW.person_id,
            (SELECT v.person_id FROM vehicles v WHERE v.id = NEW.vehicle_id));
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_attachment_owner_snapshot ON attachments;
CREATE TRIGGER trg_attachment_owner_snapshot
    BEFORE INSERT ON attachments
    FOR EACH ROW EXECUTE FUNCTION parkrr_attachment_owner_snapshot();
