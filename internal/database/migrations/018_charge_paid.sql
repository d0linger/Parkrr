-- Extra charges (Zusatzkosten) can now be settled directly. A charge tied to a
-- vehicle keeps deriving its paid state from that vehicle; a standalone charge
-- (vehicle_id IS NULL) uses this own flag instead.
ALTER TABLE charges ADD COLUMN IF NOT EXISTS paid BOOLEAN NOT NULL DEFAULT false;
