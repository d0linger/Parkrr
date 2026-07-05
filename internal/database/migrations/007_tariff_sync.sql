-- Optional coupling of a tariff's monthly and yearly price (yearly = monthly*12).
-- When true, the UI keeps the two prices in sync; when false both are free.
ALTER TABLE categories ADD COLUMN IF NOT EXISTS rates_synced BOOLEAN NOT NULL DEFAULT FALSE;
