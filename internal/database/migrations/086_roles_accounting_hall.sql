-- Ausbau 35: zwei feste Rollen dazu.
--   buchhaltung  – liest alles, dazu Steuerbereich und Exporte; schreibt sonst nichts.
--   hallendienst – Planer, Übergaben, Fotos, Service und Schlüsselbuch; sieht keine Beträge.
-- Die Rechte stehen in den Routen-Wrappern (internal/server), nicht in der Oberfläche.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin', 'editor', 'reader', 'buchhaltung', 'hallendienst'));
