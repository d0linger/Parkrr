-- Kalender-Abo (Ausbau 10): je Benutzer höchstens ein geheimer Feed-Schlüssel.
-- Gespeichert wird nur der SHA-256-Hash; der Schlüssel selbst wird einmal angezeigt.
-- Neu erzeugen ersetzt den alten Schlüssel, Löschen widerruft das Abo.
CREATE TABLE IF NOT EXISTS calendar_feeds (
    user_id    BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
