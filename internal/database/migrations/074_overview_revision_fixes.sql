-- Two gaps in 072's overview revision:
--
-- 1. The sequence starts un-called, so the first nextval returns the START value
--    and leaves last_value unchanged: the first committed write after install
--    did not invalidate the cache. Marking the current value as called makes
--    every later nextval advance last_value. A sequence that has already been
--    used keeps its value.
-- 2. TRUNCATE bypasses row-level DML, so it did not fire the statement trigger.
SELECT setval('overview_revision_seq', last_value, true) FROM overview_revision_seq;

DO $$
DECLARE
    table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'persons',
        'categories',
        'vehicles',
        'flat_rate_periods',
        'flat_rate_period_vehicles',
        'flat_rate_period_payments',
        'charges',
        'recurring_charges',
        'payments',
        'invoices',
        'invoice_source'
    ]
    LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS overview_revision_bump ON %I', table_name);
        EXECUTE format(
            'CREATE TRIGGER overview_revision_bump AFTER INSERT OR UPDATE OR DELETE OR TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION parkrr_bump_overview_revision()',
            table_name
        );
    END LOOP;
END;
$$;
