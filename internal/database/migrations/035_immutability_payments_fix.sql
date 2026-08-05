-- Fixes to the payments immutability guard (migration 034):
--
-- 1) payments.created_by is BIGINT REFERENCES users(id) ON DELETE SET NULL
--    (migration 023). Deleting an admin user fires an internal
--    UPDATE payments SET created_by = NULL for every payment they booked; the
--    034 allow-list did not subtract created_by, so the guard rejected that FK
--    action and made any such user undeletable. Tolerate created_by, exactly as
--    parkrr_guard_invoices already does (an FK null-out is not tampering).
--
-- 2) The 034 auto-payment exemption was unconditional (RETURN for any op), so an
--    auto row — which still counts as money-in in every balance sum — was fully
--    mutable at the DB level. Auto rows are system-managed toggle state: they may
--    be removed wholesale (toggle-off is a DELETE), but an UPDATE must still go
--    through the same allow-list as a manual payment (only the reversal flags /
--    the FK-nulled created_by). The app never UPDATEs an auto row outside that
--    set, so this only closes the defence-in-depth hole.

CREATE OR REPLACE FUNCTION parkrr_guard_payments() RETURNS trigger AS $$
BEGIN
	IF parkrr_purge_allowed() THEN
		RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
	END IF;
	-- Auto-payments (paid-toggle rows, migration 028) may be removed wholesale;
	-- their UPDATEs still fall through to the allow-list below.
	IF COALESCE(OLD.auto, false) AND TG_OP = 'DELETE' THEN
		RETURN OLD;
	END IF;
	IF TG_OP = 'DELETE' THEN
		RAISE EXCEPTION 'payment % is immutable (BAO §131/§132): reverse it, do not delete', OLD.id
			USING ERRCODE = 'restrict_violation';
	END IF;
	IF (to_jsonb(OLD) - 'reversed' - 'reversed_at' - 'reversed_by' - 'created_by')
	   IS DISTINCT FROM (to_jsonb(NEW) - 'reversed' - 'reversed_at' - 'reversed_by' - 'created_by') THEN
		RAISE EXCEPTION 'payment % is immutable (BAO §131): only reversal state may change', OLD.id
			USING ERRCODE = 'restrict_violation';
	END IF;
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;
