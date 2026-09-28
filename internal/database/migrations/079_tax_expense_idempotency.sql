-- Retry-safe tax-expense creation. The actor is stored separately from
-- created_by so deleting a user cannot collapse or re-scope historical keys.
ALTER TABLE tax_expenses ADD COLUMN IF NOT EXISTS idempotency_actor BIGINT;
ALTER TABLE tax_expenses ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE tax_expenses ADD COLUMN IF NOT EXISTS request_fingerprint TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_tax_expenses_actor_idempotency
    ON tax_expenses (COALESCE(idempotency_actor, 0), idempotency_key)
    WHERE idempotency_key IS NOT NULL;
