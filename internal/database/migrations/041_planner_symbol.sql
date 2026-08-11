-- Per-vehicle Garagenplaner symbol override: pick a specific top-view symbol
-- instead of the one auto-derived from the category. NULL/empty = automatic
-- (category-based). Purely a display preference — no billing effect.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS planner_symbol TEXT;
