-- 016: index the vehicle side of the agreement<->vehicle join.
-- flat_rate_period_vehicles has only PRIMARY KEY (period_id, vehicle_id), whose
-- B-tree is keyed on period_id first and so cannot serve lookups by vehicle_id
-- alone. Both the Pauschale-expiry archival sweep (correlated j.vehicle_id = v.id
-- subqueries) and the ON DELETE CASCADE from vehicles look rows up by vehicle_id,
-- so give that column its own index.
CREATE INDEX IF NOT EXISTS idx_frpv_vehicle ON flat_rate_period_vehicles(vehicle_id);
