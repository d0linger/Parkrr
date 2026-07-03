-- 2FA hardening: one-time backup/recovery codes (stored hashed).
-- The totp_secret column now holds an AES-GCM ciphertext instead of plaintext.
CREATE TABLE IF NOT EXISTS totp_backup_codes (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_backup_user ON totp_backup_codes(user_id);
