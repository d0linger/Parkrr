-- Tariffs and services can be archived: hidden from the pickers (new vehicle,
-- agreement, "Aus Katalog") while staying valid for existing records. Vehicle
-- rates are locked and charges are snapshots, so archiving never alters history.
ALTER TABLE categories    ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE service_types ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT false;
