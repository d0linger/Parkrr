-- Übergabeprotokolle sind unterschriebene Belege (Zustand + gezeichnete Unterschrift
-- bei Ein- und Auslagerung). Sie hatten bisher kein Gegenstück zum
-- Unveränderbarkeits-Trigger der Rechnungen aus 034 (Hundert SEC-79).
--
-- Bewusst NUR gegen UPDATE: handover_protocols.vehicle_id hängt an
-- ON DELETE CASCADE, ein DELETE-Guard würde also das Löschen eines Gefährts
-- mitblockieren. Das Löschen wird stattdessen auf der Route auf Admins verengt.
-- Heute existiert gar kein Update-Endpunkt, der Trigger ist damit reine
-- Tiefenverteidigung: verhaltensneutral für die App, aber ein nachträgliches
-- Verbiegen einer Unterschrift (auch direkt in der DB) schlägt fehl.
--
-- created_by ist ausgenommen, weil das Löschen eines Benutzers es über
-- ON DELETE SET NULL nullt — eine FK-Aktion, keine Manipulation. Wie in 034 wird
-- die Erlaubnisliste durch Subtraktion aus to_jsonb() gebildet, damit jede später
-- ergänzte Spalte automatisch unveränderlich ist (fail-closed).

CREATE OR REPLACE FUNCTION parkrr_guard_handovers() RETURNS trigger AS $$
BEGIN
    IF parkrr_purge_allowed() THEN
        RETURN NEW;
    END IF;
    IF (to_jsonb(OLD) - 'created_by') IS DISTINCT FROM (to_jsonb(NEW) - 'created_by') THEN
        RAISE EXCEPTION 'handover protocol % is immutable: it records a signature, issue a new protocol instead', OLD.id
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_handovers ON handover_protocols;
CREATE TRIGGER trg_guard_handovers
    BEFORE UPDATE ON handover_protocols
    FOR EACH ROW EXECUTE FUNCTION parkrr_guard_handovers();
