-- 009: WebAuthn / passkey credentials. Only public keys and metadata are stored
-- (there is no secret to protect); the private key never leaves the user device.
CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id               BIGSERIAL PRIMARY KEY,
    user_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id    BYTEA NOT NULL UNIQUE,
    public_key       BYTEA NOT NULL,
    attestation_type TEXT   NOT NULL DEFAULT '',
    aaguid           BYTEA,
    sign_count       BIGINT NOT NULL DEFAULT 0,
    transports       TEXT   NOT NULL DEFAULT '',
    backup_eligible  BOOLEAN NOT NULL DEFAULT FALSE,
    backup_state     BOOLEAN NOT NULL DEFAULT FALSE,
    name             TEXT   NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_webauthn_user ON webauthn_credentials(user_id);
