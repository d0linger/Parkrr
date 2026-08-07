-- BAO §131/§132 Unveränderbarkeit: enforce document immutability in the database
-- itself, not only in application code (defence in depth behind the in-tx audit
-- trail, migration C7). Issued invoices, their line items, and booked payments
-- are records of account: they may never be DELETEd, and their document fields
-- may never be UPDATEd. The ONLY sanctioned mutations are the settlement/storno
-- bookkeeping the app already performs:
--   invoices      : paid_amount, canceled            (allocation + Storno)
--                   created_by is also tolerated — an admin user deletion nulls
--                   it via ON DELETE SET NULL; that FK action is not tampering.
--   payments      : reversed, reversed_at, reversed_by (Storno of a money-in)
--   invoice_items : nothing — insert-only; a correction issues a NEW negated
--                   document (Storno), it never edits the original's lines.
--
-- Escape hatch: a deliberate, in-transaction  SET LOCAL parkrr.purge = 'on'
-- lifts the guard for the one legitimate exception — purging records after the
-- statutory 7-year retention window (and controlled test teardown). Absent that
-- flag every DELETE and every protected-column UPDATE is rejected.
--
-- The allow-list is expressed by subtracting the permitted keys from to_jsonb()
-- of OLD and NEW and comparing the remainder, so any column added in a later
-- migration is immutable by default (fail-closed).

CREATE OR REPLACE FUNCTION parkrr_purge_allowed() RETURNS boolean AS $$
	SELECT current_setting('parkrr.purge', true) = 'on';
$$ LANGUAGE sql STABLE;

CREATE OR REPLACE FUNCTION parkrr_guard_invoices() RETURNS trigger AS $$
BEGIN
	IF parkrr_purge_allowed() THEN
		RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
	END IF;
	IF TG_OP = 'DELETE' THEN
		RAISE EXCEPTION 'invoice % is immutable (BAO §131/§132): issue a Storno, do not delete', OLD.id
			USING ERRCODE = 'restrict_violation';
	END IF;
	IF (to_jsonb(OLD) - 'paid_amount' - 'canceled' - 'created_by')
	   IS DISTINCT FROM (to_jsonb(NEW) - 'paid_amount' - 'canceled' - 'created_by') THEN
		RAISE EXCEPTION 'invoice % document fields are immutable (BAO §131): only settlement state may change', OLD.id
			USING ERRCODE = 'restrict_violation';
	END IF;
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION parkrr_guard_payments() RETURNS trigger AS $$
BEGIN
	IF parkrr_purge_allowed() THEN
		RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
	END IF;
	-- Auto-payments are system-managed reflections of a paid-toggle (migration
	-- 028): toggling the item open removes them. They are not manually-booked
	-- receipts, so they are exempt — only real money-in is a record of account.
	-- A manual payment cannot be laundered into this exemption: flipping `auto`
	-- is itself a protected-column change, rejected below.
	IF COALESCE(OLD.auto, false) THEN
		RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
	END IF;
	IF TG_OP = 'DELETE' THEN
		RAISE EXCEPTION 'payment % is immutable (BAO §131/§132): reverse it, do not delete', OLD.id
			USING ERRCODE = 'restrict_violation';
	END IF;
	IF (to_jsonb(OLD) - 'reversed' - 'reversed_at' - 'reversed_by')
	   IS DISTINCT FROM (to_jsonb(NEW) - 'reversed' - 'reversed_at' - 'reversed_by') THEN
		RAISE EXCEPTION 'payment % is immutable (BAO §131): only reversal state may change', OLD.id
			USING ERRCODE = 'restrict_violation';
	END IF;
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION parkrr_guard_invoice_items() RETURNS trigger AS $$
BEGIN
	IF parkrr_purge_allowed() THEN
		RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
	END IF;
	RAISE EXCEPTION 'invoice_items are immutable (BAO §131): a line item cannot be %', lower(TG_OP)
		USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_invoices ON invoices;
CREATE TRIGGER trg_guard_invoices BEFORE UPDATE OR DELETE ON invoices
	FOR EACH ROW EXECUTE FUNCTION parkrr_guard_invoices();

DROP TRIGGER IF EXISTS trg_guard_payments ON payments;
CREATE TRIGGER trg_guard_payments BEFORE UPDATE OR DELETE ON payments
	FOR EACH ROW EXECUTE FUNCTION parkrr_guard_payments();

DROP TRIGGER IF EXISTS trg_guard_invoice_items ON invoice_items;
CREATE TRIGGER trg_guard_invoice_items BEFORE UPDATE OR DELETE ON invoice_items
	FOR EACH ROW EXECUTE FUNCTION parkrr_guard_invoice_items();
