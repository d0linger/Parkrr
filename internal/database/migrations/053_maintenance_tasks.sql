-- Einmal-Aufgaben ("Backfills") liefen bisher bei JEDEM Start: der Zahlungs-Backfill
-- lädt dazu sämtliche Vereinbarungen und wiederkehrenden Posten in den Speicher und
-- prüft jede Teilperiode einzeln, obwohl er nach dem ersten erfolgreichen Durchlauf
-- nichts mehr zu tun hat (Hundert 08). Diese Tabelle merkt sich, dass er fertig ist.
--
-- Bewusst eine eigene Tabelle statt einer Spalte in schema_migrations: Migrationen
-- laufen im Schema-Schritt und sind DDL, diese Marker gehören zur Laufzeit und
-- dürfen unabhängig davon gesetzt und (durch den Betreiber) zurückgesetzt werden.
CREATE TABLE IF NOT EXISTS maintenance_tasks (
    task    TEXT PRIMARY KEY,
    done_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- BEWUSST OHNE Vorbelegung: den Marker hier gleich zu setzen hieße anzunehmen, dass
-- der Backfill in dieser Installation je erfolgreich durchgelaufen ist. Lief er
-- bisher bei jedem Start in denselben Fehler, würde ein vorbelegter Marker die
-- Nacharbeit für immer überspringen. So läuft er genau ein letztes Mal — wie heute
-- auch — und trägt sich erst nach Erfolg selbst ein.
