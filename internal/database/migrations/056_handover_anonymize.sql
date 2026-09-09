-- Die DSGVO-Löschung schrubbte bisher NUR die Personenzeile. Die gezeichnete
-- Unterschrift und der Name des Unterzeichners im Übergabeprotokoll blieben stehen
-- (Hundert 39) — und das ist der personenbezogenste Datenpunkt der Anwendung.
--
-- Der Konflikt ist echt: Migration 051 macht Übergabeprotokolle unveränderlich, weil
-- sie unterschriebene Belege sind. Genau dieselbe Zeile soll die Löschung ändern.
--
-- Aufgelöst NICHT über parkrr.purge: das ist der Generalschlüssel, mit dem der
-- Teardown der Tests jede Unveränderlichkeit aushebelt. Ihn in der Anwendung zu
-- benutzen hieße, dem Anwendungscode diesen Generalschlüssel in die Hand zu geben.
--
-- Stattdessen ein eigener, ENGERER Schalter: parkrr.anonymize erlaubt ausschließlich
-- das Ändern von signer_name und signature — jede andere Abweichung schlägt weiter
-- fehl, auch während der Anonymisierung. Der Beleg bleibt damit als Beleg erhalten
-- (Richtung, Datum, Zustandsnotizen, Bezug zum Gefährt); es verschwindet die Person.
CREATE OR REPLACE FUNCTION parkrr_anonymize_allowed() RETURNS boolean AS $$
    SELECT current_setting('parkrr.anonymize', true) = 'on';
$$ LANGUAGE sql STABLE;

CREATE OR REPLACE FUNCTION parkrr_guard_handovers() RETURNS trigger AS $$
BEGIN
    IF parkrr_purge_allowed() THEN
        RETURN NEW;
    END IF;
    -- Wie in 051 wird die Erlaubnisliste durch SUBTRAKTION aus to_jsonb() gebildet:
    -- jede später ergänzte Spalte ist damit automatisch unveränderlich (fail-closed).
    IF parkrr_anonymize_allowed() THEN
        IF (to_jsonb(OLD) - 'created_by' - 'signer_name' - 'signature')
           IS DISTINCT FROM (to_jsonb(NEW) - 'created_by' - 'signer_name' - 'signature') THEN
            RAISE EXCEPTION 'handover protocol %: anonymizing may only clear signer_name and signature', OLD.id
                USING ERRCODE = 'restrict_violation';
        END IF;
        RETURN NEW;
    END IF;
    IF (to_jsonb(OLD) - 'created_by') IS DISTINCT FROM (to_jsonb(NEW) - 'created_by') THEN
        RAISE EXCEPTION 'handover protocol % is immutable: it records a signature, issue a new protocol instead', OLD.id
            USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
