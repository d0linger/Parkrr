-- One-off charges cannot be owned by both a payment and an active invoice.
-- Refuse existing conflicts: reconcile explicitly without deleting financial evidence.
-- The migration runner owns the transaction and locks prevent concurrent installation races.
LOCK TABLE invoice_source, payment_allocations IN SHARE ROW EXCLUSIVE MODE;

DO $$
BEGIN
 IF EXISTS (
  SELECT 1 FROM invoice_source s
  JOIN invoices i ON i.id=s.invoice_id
  JOIN payment_allocations a ON a.kind=s.kind AND a.ref_id=s.ref_id
  WHERE s.kind='charge' AND s.period_key='' AND NOT i.canceled
 ) THEN
  RAISE EXCEPTION 'existing charge invoice/payment conflicts require reconciliation';
 END IF;
END $$;

CREATE FUNCTION parkrr_guard_charge_claim() RETURNS trigger
LANGUAGE plpgsql VOLATILE AS $$
BEGIN
 IF NEW.kind <> 'charge' THEN RETURN NEW; END IF;
 IF TG_TABLE_NAME = 'invoice_source' THEN
  IF NEW.period_key <> '' THEN RETURN NEW; END IF;
 END IF;
 IF current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION 'charge claim guard requires read committed isolation'
   USING ERRCODE='25001';
 END IF;

 -- Separate SQL statements matter: after waiting, VOLATILE/READ COMMITTED
 -- rechecks the other table with a fresh snapshot before permitting this claim.
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

CREATE TRIGGER parkrr_guard_charge_invoice BEFORE INSERT OR UPDATE ON invoice_source
 FOR EACH ROW EXECUTE FUNCTION parkrr_guard_charge_claim();
CREATE TRIGGER parkrr_guard_charge_payment BEFORE INSERT OR UPDATE ON payment_allocations
 FOR EACH ROW EXECUTE FUNCTION parkrr_guard_charge_claim();
