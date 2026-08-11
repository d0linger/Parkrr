-- Physical dimensions for the Garagenplaner's dimension-matching (vehicle height
-- vs hall gate height, weight vs floor load, footprint vs free area). Purely
-- organisational — no billing impact. All nullable: a Gefährt without measured
-- dimensions simply skips the fit checks (treated as "unknown, no warning").
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS length_m NUMERIC(6,2);
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS width_m  NUMERIC(6,2);
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS height_m NUMERIC(6,2);
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS weight_t NUMERIC(7,2);

-- Guard against nonsense/DoS values while allowing real caravans/boats.
ALTER TABLE vehicles ADD CONSTRAINT chk_vehicle_dims CHECK (
    (length_m IS NULL OR (length_m > 0 AND length_m <= 60)) AND
    (width_m  IS NULL OR (width_m  > 0 AND width_m  <= 15)) AND
    (height_m IS NULL OR (height_m > 0 AND height_m <= 15)) AND
    (weight_t IS NULL OR (weight_t >= 0 AND weight_t <= 200))
);
