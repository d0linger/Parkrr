-- Die tote Tabelle flatrate_paid_years entfernen (Hundert 33) — BEWUSST als
-- letzter Punkt des Programms, weil es der einzige destruktive ist.
--
-- Befund: seit Migration 012 (flat_rate_periods) liest und schreibt KEIN Code
-- mehr in diese Tabelle — kein Handler, kein Frontend, keine Abfrage. Sie wurde
-- damals "zur Sicherheit" stehen gelassen und ist seither eingefrorene
-- Vergangenheit, die in jedem Backup, jedem Schema-Diff und jeder Migrations-
-- prüfung mitläuft.
--
-- Sicherheitsnetz statt blindem DROP: sollten in einer Installation noch Zeilen
-- liegen (der 006er-Backfill hat welche erzeugt), wandern sie als EIN
-- Audit-Eintrag ins Änderungsprotokoll — die historische Aussage ("Jahr X der
-- alten Pauschale galt als bezahlt") bleibt damit nachlesbar, nur die Tabelle
-- verschwindet. Die Datenmenge ist konstruktionsbedingt winzig (Person × Jahr).
DO $$
DECLARE
    archived JSONB;
    n        INT;
BEGIN
    SELECT count(*), COALESCE(jsonb_agg(jsonb_build_object(
               'person_id', person_id, 'year', year, 'created_at', created_at)), '[]'::jsonb)
      INTO n, archived
      FROM flatrate_paid_years;
    IF n > 0 THEN
        INSERT INTO audit_log (username, action, entity, entity_id, summary, changes)
        VALUES ('system', 'delete', 'system', 0,
                'Migration 064: Alt-Tabelle flatrate_paid_years entfernt — ' || n || ' historische Zeile(n) hier archiviert',
                jsonb_build_object('flatrate_paid_years', jsonb_build_object('old', archived, 'new', NULL)));
    END IF;
END $$;

DROP TABLE IF EXISTS flatrate_paid_years;
