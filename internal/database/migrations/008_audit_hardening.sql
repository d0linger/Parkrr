-- 008: audit-log hardening + a missing index.

-- Speed up the charges<->vehicles "paid" join used by the statistics queries
-- (there is already an index on charges.person_id, but not on vehicle_id).
CREATE INDEX IF NOT EXISTS idx_charges_vehicle ON charges(vehicle_id);

-- Make the audit log append-only for tamper-evidence: UPDATE is always blocked,
-- and DELETE is blocked unless a trusted retention job explicitly opts in for
-- the current transaction via the parkrr.allow_audit_prune session flag. This
-- holds regardless of the connecting role (it cannot be bypassed by the app's
-- normal DB user, even though that user owns the table).
CREATE OR REPLACE FUNCTION audit_log_guard() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE'
       AND current_setting('parkrr.allow_audit_prune', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'audit_log is append-only (% not permitted)', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_log_guard_trg ON audit_log;
CREATE TRIGGER audit_log_guard_trg
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_guard();
