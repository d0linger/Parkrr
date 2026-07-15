-- Bind a recurring extra cost to a vehicle, mirroring one-off charges: an
-- optional vehicle_id means the cost is attributed to that Gefährt and settled
-- via its covering Pauschale (or the vehicle's own paid flag) instead of the
-- charge's own per-period flags. NULL keeps the person-level, directly-payable
-- behavior. ON DELETE SET NULL so removing a vehicle detaches, never deletes.
ALTER TABLE recurring_charges
    ADD COLUMN IF NOT EXISTS vehicle_id BIGINT REFERENCES vehicles(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_recurring_charges_vehicle ON recurring_charges(vehicle_id);
