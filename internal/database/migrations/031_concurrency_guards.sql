-- P0 concurrency hardening: enforce single-billing / single-settlement at the DB
-- level so racing operations (double-click, two operators, retries) can't
-- double-bill a position or double-allocate a payment.
--
--  * invoice_source: a position (kind, ref_id, period_key) may appear on at most
--    ONE active invoice. Canceling an invoice DELETEs its invoice_source rows
--    (see CancelInvoice), so a plain unique index is correct — only active
--    invoices contribute, and a Storno frees the position for re-billing.
--  * payment_allocations: a position (kind, ref_id) is settled by at most one
--    payment at a time. Deleting a payment cascades its allocations, freeing the
--    position again.
CREATE UNIQUE INDEX IF NOT EXISTS uq_invoice_source_ref_period
    ON invoice_source (kind, ref_id, period_key);

CREATE UNIQUE INDEX IF NOT EXISTS uq_payalloc_ref
    ON payment_allocations (kind, ref_id);
