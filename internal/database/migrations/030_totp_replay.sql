-- L3: TOTP replay protection. A time-based code stays valid for its ~30s window
-- (plus skew), so an observed code could be replayed. We record the 30-second
-- time-step of the last accepted code and reject any code from that step or
-- earlier, making each code single-use.
ALTER TABLE users ADD COLUMN IF NOT EXISTS last_totp_step BIGINT NOT NULL DEFAULT 0;
