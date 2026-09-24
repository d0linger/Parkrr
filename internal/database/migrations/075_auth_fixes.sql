-- Auth-Härtung (Audit AUTH-02, AUTH-03).
--
-- 1. Benutzernamen eindeutig OHNE Rücksicht auf Groß-/Kleinschreibung.
--    Die Anmeldung sucht mit lower(username) = lower($1), die Eindeutigkeit aus
--    001 prüfte aber case-sensitiv. "Bob" und "bob" konnten nebeneinander
--    existieren, und die Anmeldung griff dann eine beliebige der beiden Zeilen.
--
--    Bestehende Kollisionen werden VOR dem Index aufgelöst, sonst schlüge die
--    Migration auf genau den Installationen fehl, die sie braucht. Regel,
--    deterministisch: je Gruppe gleicher lower(username) behält das älteste
--    Konto (kleinste id) seinen Namen; jedes weitere wird umbenannt in
--    "<name>-dup<id>" (Name auf 80 Zeichen gekürzt, damit das Ergebnis unter der
--    100-Zeichen-Grenze der Anmeldung bleibt). Wäre auch dieser Name belegt, kommt
--    ein weiterer Zähler "-<n>" dazu. Konten, Passwörter, Sitzungen und alle
--    Verweise (per id) bleiben unverändert; nur der Anmeldename der Dubletten
--    ändert sich. Jede Umbenennung steht als Eintrag im Änderungsprotokoll, damit
--    der Betreiber die Betroffenen informieren kann.
DO $$
DECLARE
    r    RECORD;
    cand TEXT;
    n    INT;
BEGIN
    FOR r IN
        SELECT id, username FROM (
            SELECT id, username,
                   row_number() OVER (PARTITION BY lower(username) ORDER BY id) AS rn
              FROM users
        ) d
        WHERE rn > 1
        ORDER BY id
    LOOP
        cand := left(r.username, 80) || '-dup' || r.id;
        n := 0;
        WHILE EXISTS (SELECT 1 FROM users WHERE lower(username) = lower(cand)) LOOP
            n := n + 1;
            cand := left(r.username, 80) || '-dup' || r.id || '-' || n;
        END LOOP;
        UPDATE users SET username = cand, updated_at = now() WHERE id = r.id;
        INSERT INTO audit_log (username, action, entity, entity_id, summary, changes)
        VALUES ('system', 'update', 'user', r.id,
                'Migration 075: Benutzername ' || r.username || ' kollidierte ohne Groß-/Kleinschreibung und wurde in ' || cand || ' umbenannt',
                jsonb_build_object('username', jsonb_build_object('old', r.username, 'new', cand)));
        RAISE NOTICE 'migration 075: renamed duplicate username % (id %) to %', r.username, r.id, cand;
    END LOOP;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_key ON users (lower(username));

-- 2. Fehlversuche beim ZWEITEN Faktor, dauerhaft je Konto (AUTH-03).
--    Der Zähler steigt nur nach korrektem Passwort und wird ausschließlich durch
--    einen erfolgreichen zweiten Faktor (oder einen Admin-Reset der 2FA) auf 0
--    gesetzt — nicht durch eine bloße Passwortanmeldung und nicht durch einen
--    Neustart (der In-Memory-Begrenzer vergisst beides). totp_locked_until ist
--    die daraus berechnete, eskalierende Sperre.
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_failures INT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_locked_until TIMESTAMPTZ;
