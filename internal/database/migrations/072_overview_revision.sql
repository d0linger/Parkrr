-- Cross-replica invalidation for the in-process dashboard overview cache.
-- A sequence is deliberately used instead of a singleton counter row: updating
-- one row from every write transaction creates a global lock-order edge and can
-- deadlock otherwise-independent invoice/payment transactions. Sequence values
-- are non-transactional, so a rolled-back write can cause a harmless extra cache
-- miss, while committed writes are always observed by every replica.
CREATE SEQUENCE overview_revision_seq AS bigint START WITH 1;

CREATE OR REPLACE FUNCTION parkrr_bump_overview_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM nextval('overview_revision_seq');
    RETURN NULL;
END;
$$;

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
        EXECUTE format(
            'CREATE TRIGGER overview_revision_bump AFTER INSERT OR UPDATE OR DELETE ON %I FOR EACH STATEMENT EXECUTE FUNCTION parkrr_bump_overview_revision()',
            table_name
        );
    END LOOP;
END;
$$;
