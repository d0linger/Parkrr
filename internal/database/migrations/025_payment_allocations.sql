-- Links a payment to the individual open positions it settled, so payments and
-- the per-item "paid" status become interdependent: a payment stamps items, and
-- deleting the payment reverts exactly those it set. The unallocated remainder of
-- a payment (amount − Σ allocations) is the person's Guthaben (credit).
--
-- Only the boolean-settled items are allocated here (kind 'vehicle' | 'charge');
-- Pauschale/recurring per-period payments keep their own existing mechanism.

CREATE TABLE IF NOT EXISTS payment_allocations (
    id         BIGSERIAL PRIMARY KEY,
    payment_id BIGINT NOT NULL REFERENCES payments(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('vehicle','charge')),
    ref_id     BIGINT NOT NULL,
    amount     NUMERIC(12,2) NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_payalloc_payment ON payment_allocations(payment_id);
CREATE INDEX IF NOT EXISTS idx_payalloc_ref ON payment_allocations(kind, ref_id);
