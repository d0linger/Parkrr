-- GUI-editable per-target backup schedule (single row) + runtime status.
-- S3 credentials + encryption key stay in the environment; only the schedule,
-- retention, and run state live here so they can be managed from the Backup tab.

CREATE TABLE IF NOT EXISTS backup_settings (
    id          INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    volume_cron TEXT NOT NULL DEFAULT '0 3 * * *', -- 5-field cron; empty = off
    volume_keep INT  NOT NULL DEFAULT 14,
    s3_cron     TEXT NOT NULL DEFAULT '0 4 * * *',
    s3_keep     INT  NOT NULL DEFAULT 0             -- 0 = keep all
);
INSERT INTO backup_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS backup_status (
    id                INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_volume_at    TIMESTAMPTZ,
    last_volume_size  BIGINT      NOT NULL DEFAULT 0,
    last_volume_ok    BOOLEAN     NOT NULL DEFAULT FALSE,
    restore_tested_at TIMESTAMPTZ,
    last_s3_at        TIMESTAMPTZ,
    last_s3_ok        BOOLEAN     NOT NULL DEFAULT FALSE
);
INSERT INTO backup_status (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
