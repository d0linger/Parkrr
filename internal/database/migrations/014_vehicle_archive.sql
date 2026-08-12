-- 014: archive closed vehicles to declutter the active lists.
-- A vehicle is "closed" when it is cancelled, or collected AND paid. Closed
-- vehicles auto-archive and become read-only until manually reactivated.
-- Archiving is purely a list/edit concern: archived vehicles still count in all
-- billing and statistics, so this changes nothing about the figures.
-- Backfill already-closed vehicles so the existing clutter is tidied away at
-- once; anything still open (e.g. collected but unpaid) stays active.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT false;

UPDATE vehicles
SET archived = true
WHERE archived = false
  AND (status = 'cancelled' OR (status = 'collected' AND paid));

CREATE INDEX IF NOT EXISTS idx_vehicles_archived ON vehicles(archived);
