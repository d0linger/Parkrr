-- Server-side WebAuthn ceremony state (finding SH-03). The begin/finish challenge
-- was previously carried entirely in an encrypted cookie, so it never expired
-- server-side and was not atomically single-use (a captured cookie could be
-- replayed, especially with counterless authenticators). Store the ceremony
-- server-side instead; the cookie now carries only an opaque, unguessable id.
-- Each row is consumed exactly once (consumed_at) under a row lock, and expires.
CREATE TABLE IF NOT EXISTS webauthn_ceremonies (
    id          TEXT PRIMARY KEY,
    data        BYTEA NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_webauthn_ceremonies_expires ON webauthn_ceremonies (expires_at);
