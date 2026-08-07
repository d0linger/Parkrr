-- B1-Rest: period-scoped Fakturier-Sperre for periodic positions.
--  * Discrete positions (vehicle, one-off charge) keep period_key='' and lock
--    wholesale, exactly as before.
--  * Periodic positions (Pauschale = agreement, recurring Nebenkosten) now lock
--    per COMPLETED sub-period key ('YYYY-MM' monthly / 'YYYY' yearly), so each
--    period is billed exactly once across all active invoices. Released on Storno
--    (the lock ignores canceled invoices).

ALTER TABLE invoice_source ADD COLUMN IF NOT EXISTS period_key TEXT NOT NULL DEFAULT '';

-- Widen the kind whitelist to the two periodic kinds.
ALTER TABLE invoice_source DROP CONSTRAINT IF EXISTS invoice_source_kind_check;
ALTER TABLE invoice_source ADD  CONSTRAINT invoice_source_kind_check
    CHECK (kind IN ('vehicle', 'charge', 'agreement', 'recurring'));

CREATE INDEX IF NOT EXISTS idx_invoice_source_refperiod ON invoice_source(kind, ref_id, period_key);
