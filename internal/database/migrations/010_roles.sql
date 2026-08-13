-- 010: consolidate roles to admin / editor / reader.
--   manager, accounting  -> editor  (everything except users & audit log)
--   readonly             -> reader
-- Order matters: drop the old CHECK, remap existing values, then add the new
-- CHECK so no row ever violates it mid-migration. On a fresh install only the
-- bootstrap admin exists, so the UPDATEs are no-ops.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;

UPDATE users SET role = 'editor' WHERE role IN ('manager', 'accounting');
UPDATE users SET role = 'reader' WHERE role = 'readonly';

ALTER TABLE users ALTER COLUMN role SET DEFAULT 'editor';
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin', 'editor', 'reader'));
