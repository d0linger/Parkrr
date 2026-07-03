-- Simple per-vehicle payment marker (open vs. paid) for slider-based workflow.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS paid BOOLEAN NOT NULL DEFAULT FALSE;
