-- Invoice lifecycle for GoBD/BAO conformance:
--  * Unveränderbarkeit: invoices are never deleted; a Storno creates a cancelling
--    document (cancels_id -> original) and marks the original canceled.
--  * Keine Doppel-Fakturierung: invoice_source locks the discrete positions
--    (vehicles, one-off charges) that an active (non-canceled) invoice already
--    billed, so they can't be put on a second invoice. Released on cancel.
--    (Pauschale/recurring are periodic and not yet locked — see handler note.)

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS canceled   BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS cancels_id BIGINT REFERENCES invoices(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS invoice_source (
    id         BIGSERIAL PRIMARY KEY,
    invoice_id BIGINT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    kind       TEXT   NOT NULL CHECK (kind IN ('vehicle','charge')),
    ref_id     BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_invoice_source_ref     ON invoice_source(kind, ref_id);
CREATE INDEX IF NOT EXISTS idx_invoice_source_invoice ON invoice_source(invoice_id);
