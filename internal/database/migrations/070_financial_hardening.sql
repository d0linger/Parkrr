-- Financial hardening for the 2026-09 static-audit remediation.

-- Once an agreement has produced settlement evidence, its billing definition is
-- immutable.  Re-opening a paid toggle removes the current allocation, but does
-- not make historical prices or service windows safe to rewrite.
ALTER TABLE flat_rate_periods
    ADD COLUMN IF NOT EXISTS settlement_locked BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE flat_rate_periods f
   SET settlement_locked = TRUE
 WHERE f.paid
    OR EXISTS (SELECT 1 FROM flat_rate_period_payments pp WHERE pp.period_id = f.id)
    OR EXISTS (
        SELECT 1 FROM payments p
         WHERE p.settles_kind = 'agreement'
           AND p.settles_ref = f.id
    );

-- Payment creation uses a caller-supplied idempotency key.  The authenticated
-- actor scopes the key; COALESCE keeps integration/system callers deterministic.
ALTER TABLE payments ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS request_fingerprint TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_payments_actor_idempotency
    ON payments (COALESCE(created_by, 0), idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Cross-table charge claims use an advisory lock plus a fresh READ COMMITTED
-- statement snapshot. SERIALIZABLE is intentionally rejected by the handler:
-- it can legally retain a snapshot from before an opposing read-committed claim.
CREATE OR REPLACE FUNCTION parkrr_guard_charge_claim() RETURNS trigger
LANGUAGE plpgsql VOLATILE AS $$
BEGIN
 IF NEW.kind <> 'charge' THEN RETURN NEW; END IF;
 IF TG_TABLE_NAME = 'invoice_source' THEN
  IF NEW.period_key <> '' THEN RETURN NEW; END IF;
 END IF;
 IF current_setting('transaction_isolation') NOT IN ('read committed', 'serializable') THEN
  RAISE EXCEPTION 'charge claim guard requires read committed or serializable isolation'
   USING ERRCODE='25001';
 END IF;

 PERFORM pg_advisory_xact_lock(hashtextextended('parkrr.charge-claim:' || NEW.ref_id::text, 0));
 IF TG_TABLE_NAME = 'invoice_source' THEN
  IF EXISTS (SELECT 1 FROM payment_allocations WHERE kind='charge' AND ref_id=NEW.ref_id) THEN
   RAISE EXCEPTION 'charge already claimed by a payment'
    USING ERRCODE='23505', CONSTRAINT='charge_claim_exclusive';
  END IF;
 ELSE
  IF EXISTS (SELECT 1 FROM invoice_source s JOIN invoices i ON i.id=s.invoice_id
             WHERE s.kind='charge' AND s.ref_id=NEW.ref_id AND s.period_key='' AND NOT i.canceled) THEN
   RAISE EXCEPTION 'charge already claimed by an invoice'
    USING ERRCODE='23505', CONSTRAINT='charge_claim_exclusive';
  END IF;
 END IF;
 RETURN NEW;
END $$;
