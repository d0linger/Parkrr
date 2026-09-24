-- Restore coordination must survive replacement of the application schema.
-- Every Parkrr backup excludes this schema, so a browser-triggered restore can
-- retain its fail-closed state and progress while pg_restore replaces public.
CREATE SCHEMA IF NOT EXISTS parkrr_control;

CREATE TABLE IF NOT EXISTS parkrr_control.restore_jobs (
    id              TEXT PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
    owner_instance  TEXT NOT NULL CHECK (owner_instance ~ '^[0-9a-f]{32}$'),
    phase           TEXT NOT NULL CHECK (phase IN (
                        'queued', 'draining', 'restoring', 'migrating',
                        'purging_sessions', 'verifying', 'recovering',
                        'complete', 'failed', 'cancelled'
                    )),
    source_name     TEXT NOT NULL,
    source_path     TEXT NOT NULL,
    backup_created  TEXT NOT NULL DEFAULT '',
    archive_entries INTEGER NOT NULL DEFAULT 0 CHECK (archive_entries >= 0),
    checksum_sha256 TEXT NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
    requested_by    TEXT NOT NULL,
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at    TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS restore_jobs_one_active
    ON parkrr_control.restore_jobs ((true))
    WHERE phase NOT IN ('complete', 'failed', 'cancelled');

CREATE INDEX IF NOT EXISTS restore_jobs_created_at
    ON parkrr_control.restore_jobs (created_at DESC);
