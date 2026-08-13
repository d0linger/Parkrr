-- Übergabeprotokoll (handover protocol): a dated record of a vehicle being
-- taken in ("einlagerung") or handed back ("auslagerung"), capturing the noted
-- condition and an optional signature image. Photos are the existing
-- vehicle_photos (shared gallery); the protocol references the vehicle, not a
-- photo copy, so a photo added around the handover is visible from both.
CREATE TABLE IF NOT EXISTS handover_protocols (
    id          BIGSERIAL PRIMARY KEY,
    vehicle_id  BIGINT NOT NULL REFERENCES vehicles(id) ON DELETE CASCADE,
    direction   TEXT NOT NULL CHECK (direction IN ('einlagerung', 'auslagerung')),
    notes       TEXT NOT NULL DEFAULT '',
    signer_name TEXT NOT NULL DEFAULT '',
    signature   BYTEA,                       -- PNG, optional (drawn on the device)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  BIGINT REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_handover_vehicle
    ON handover_protocols(vehicle_id, created_at DESC);
