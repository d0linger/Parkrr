-- P2: the invoice becomes the open item (OP). Payments are allocated to invoices;
-- paying an invoice settles EVERYTHING on it (rent, Pauschale, recurring, charges)
-- in one atomic step, so the payment covers all position types — not just the
-- boolean-settled ones. invoices.paid_amount tracks how much of the invoice is
-- covered; open = total - paid_amount; status derives from it.

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS paid_amount NUMERIC(12,2) NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS invoice_payments (
    id         BIGSERIAL PRIMARY KEY,
    invoice_id BIGINT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    payment_id BIGINT NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    amount     NUMERIC(12,2) NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_invpay_invoice ON invoice_payments(invoice_id);
CREATE INDEX IF NOT EXISTS idx_invpay_payment ON invoice_payments(payment_id);
