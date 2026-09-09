-- Die Suche im Änderungsprotokoll lief als zwei ungeankerte ILIKE '%…%' über
-- username und summary. Gemessen auf einer Testdatenbank mit 17.924 Zeilen:
--
--   Limit -> Sort -> Seq Scan on audit_log
--            Filter: username ~~* '%…%' OR summary ~~* '%…%'
--
-- Also die ganze Tabelle lesen und sortieren, für jede Eingabe. audit_log ist wegen
-- der 7-Jahres-Aufbewahrung (BAO §132) die am schnellsten wachsende Tabelle der
-- Anwendung — genau die, bei der ein Seq Scan am teuersten wird (Hundert 35).
--
-- pg_trgm kann ungeankerte ILIKE-Muster indizieren; ein B-Tree kann das nicht (er
-- braucht einen linken Anker). Die Erweiterung richtet Migration 049 bereits ein.
--
-- OHNE pg_trgm passiert hier bewusst NICHTS: die Suche bleibt dann langsam, aber
-- korrekt. Ein harter Fehler würde die gesamte Migrationskette anhalten, und zwar
-- wegen einer Beschleunigung.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        RAISE NOTICE 'parkrr: pg_trgm nicht vorhanden - Audit-Suche bleibt ohne Index';
        RETURN;
    END IF;

    -- Zur Sperre: CREATE INDEX (ohne CONCURRENTLY) haelt waehrend des Aufbaus eine
    -- SHARE-Sperre, Schreibzugriffe auf audit_log warten so lange. CONCURRENTLY ist
    -- hier keine Option, weil jede Migration in einer Transaktion laeuft.
    --
    -- Vertretbar, weil die Tabelle in dieser Anwendung in der Groessenordnung
    -- Zehntausende liegt und der Aufbau damit im Millisekundenbereich. Sollte eine
    -- Installation je in die Millionen wachsen, gehoert der Index einmalig von Hand
    -- mit CONCURRENTLY ausserhalb der Migration gebaut.
    CREATE INDEX IF NOT EXISTS idx_audit_summary_trgm  ON audit_log USING gin (summary  gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS idx_audit_username_trgm ON audit_log USING gin (username gin_trgm_ops);
END $$;
