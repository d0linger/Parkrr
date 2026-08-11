-- Ladebedarf: marks a Gefährt that needs an electrical hookup (E-vehicle, a fridge
-- in a caravan, etc.). Purely organizational for the Garagenplaner — shows a ⚡ badge
-- on the block and can later be matched to powered spots. No billing impact.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS needs_power BOOLEAN NOT NULL DEFAULT false;
