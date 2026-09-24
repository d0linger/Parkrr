-- TOTP enrollment state is separate from the active factor and bound to one
-- short-lived ceremony. A second setup can no longer swap the secret that an
-- already-open enable request will activate.
ALTER TABLE users ADD COLUMN IF NOT EXISTS pending_totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS pending_totp_nonce TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS pending_totp_expires_at TIMESTAMPTZ;
