-- Customer self-service magic-link tokens. Each row grants read-only access to
-- ONE person's own data (vehicles, open balance, invoices) via a long random
-- token delivered by link/e-mail. Only the token's SHA-256 (hex) is stored, so a
-- leaked database row cannot reconstruct a working link. Tokens expire and can be
-- revoked.
CREATE TABLE IF NOT EXISTS self_service_tokens (
    id           BIGSERIAL PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    person_id    BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    last_used_at TIMESTAMPTZ,
    revoked      BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_selfservice_person ON self_service_tokens(person_id);
