-- 049: unscharfe, gestemmte, diakritika-gefaltete globale Suche.
--
-- Zwei Erweiterungen, die die Suchabfrage benutzt:
--   pg_trgm  — Trigramm-Ähnlichkeit für Tippfehlertoleranz ("Müler" findet "Müller")
--   unaccent — Diakritika-Faltung, damit "Muller" das "Müller" findet
-- Das deutsche Stemming ("Wohnwägen" findet "Wohnwagen") kommt aus
-- to_tsvector('german', …) in der Abfrage selbst und braucht keine Schemaänderung.
--
-- KEIN harter Abbruch, wenn eine Erweiterung nicht angelegt werden kann. Seit
-- PostgreSQL 13 sind beide als "trusted" markiert, ein Benutzer mit CREATE-Recht auf
-- der Datenbank darf sie also selbst installieren — aber Parkrr liefert ein
-- docker-compose aus, das andere fahren, teils gegen eine verwaltete Datenbank mit
-- eingeschränkter Rolle. Migrationen laufen in einer Transaktion und ein Fehlschlag
-- bricht den Start ab: ohne diese Absicherung würde eine BEQUEMLICHKEIT BEI DER SUCHE
-- eine laufende Installation am Hochfahren hindern. Das ist kein vertretbarer Tausch.
--
-- Aufgefangen werden genau die drei Fehler, die "die Erweiterung ist hier nicht zu
-- haben" bedeuten. Alles andere schlägt weiterhin laut fehl.
--
-- Die Suche prüft zur Laufzeit, was wirklich installiert ist (searchCapabilities in
-- internal/handlers/search.go), und fällt sonst auf ILIKE ohne Faltung zurück: dann
-- ist sie weniger tolerant, aber sie funktioniert.
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
EXCEPTION WHEN insufficient_privilege OR undefined_file OR feature_not_supported THEN
    RAISE NOTICE 'parkrr: pg_trgm nicht verfuegbar (%) - Suche ohne Tippfehlertoleranz', SQLERRM;
END $$;

DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS unaccent;
EXCEPTION WHEN insufficient_privilege OR undefined_file OR feature_not_supported THEN
    RAISE NOTICE 'parkrr: unaccent nicht verfuegbar (%) - Suche ohne Diakritika-Faltung', SQLERRM;
END $$;

-- Bewusst KEINE Indizes. Bei einigen hundert Zeilen je Tabelle scannt der Planer die
-- kleinen Tabellen ohnehin sequenziell; to_tsvector und word_similarity im Flug zu
-- rechnen ist dabei sofort fertig, ein GIN-Index wäre nur ungenutzter Pflegeaufwand.
-- Der Weg nach oben, sollte der Bestand je in die Zehntausende gehen: ein
-- GIN-Trigramm-Index (USING gin (spalte gin_trgm_ops)) plus eine STORED generierte
-- tsvector-Spalte mit einem IMMUTABLE-Wrapper um unaccent — dann, nicht jetzt.
