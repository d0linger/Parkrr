-- Editors may delete unprotected master data, but handover deletion is admin-only.
-- Prevent parent cascades from bypassing that authorization boundary.
-- Defer the existing-row scan to a later validation migration; new writes and
-- parent deletions are still checked immediately.
ALTER TABLE handover_protocols
    DROP CONSTRAINT handover_protocols_vehicle_id_fkey,
    ADD CONSTRAINT handover_protocols_vehicle_id_fkey
        FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE RESTRICT NOT VALID;
