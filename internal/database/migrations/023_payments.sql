-- The `payments` table was reserved by 002_features.sql but never wired into the
-- application. This feature uses it as the money-in log (Kontoauszug), so bring
-- it to the shape the handlers need. Idempotent — safe on a fresh DB (002 creates
-- the table first) and on one where 002 already ran.

-- Who recorded the payment (nullable; SET NULL if the user is later deleted).
ALTER TABLE payments ADD COLUMN IF NOT EXISTS created_by BIGINT REFERENCES users(id) ON DELETE SET NULL;

-- Default method matches the UI's first option.
ALTER TABLE payments ALTER COLUMN method SET DEFAULT 'bar';

-- Reject non-positive amounts going forward (the table is empty at rollout).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'payments_amount_positive') THEN
        ALTER TABLE payments ADD CONSTRAINT payments_amount_positive CHECK (amount > 0);
    END IF;
END $$;

-- Listing index: a person's payments, newest first (002 indexes person alone).
CREATE INDEX IF NOT EXISTS idx_payments_person_paid_on ON payments(person_id, paid_on DESC);
