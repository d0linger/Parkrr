-- BAO §131/§132: a booked money-in must not vanish. The former hard DELETE of a
-- payment is replaced by a reversal (Storno) that KEEPS the record and flags it
-- reversed. Reversed payments are excluded from every money sum (balance, Guthaben,
-- Kontoauszug) but remain in the ledger for the audit trail and 7-year retention.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS reversed    BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS reversed_at TIMESTAMPTZ;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS reversed_by BIGINT REFERENCES users(id) ON DELETE SET NULL;
