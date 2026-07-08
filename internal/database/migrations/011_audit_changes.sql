-- 011: richer audit log — per-field old/new values + indexes for filtering.
-- (ADD COLUMN / CREATE INDEX are DDL and are not blocked by the append-only
-- row trigger from migration 008.)
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS changes JSONB;

CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action);
CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_log(entity);
CREATE INDEX IF NOT EXISTS idx_audit_username ON audit_log(lower(username));
