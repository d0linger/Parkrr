-- Billing audit fixes (BIL-01, BIL-02, WEB-02).
--
-- 1. Vehicle rent settles through a date, not a sticky boolean (BIL-02).
--
--    vehicles.paid used to suppress invoicing of EVERY period of the vehicle,
--    including rent that accrued long after the settlement. paid_through is the
--    last day (inclusive) of rent the settlement covers; the invoice run bills
--    everything after it per completed period, and the per-period line of the
--    period that straddles it bills only the uncovered rest. A new settlement
--    (slider, allocated payment, Guthaben drawdown) sets it to the day the money
--    was booked — exactly the accrual the allocated amount paid for.
--
--    Existing rows: a vehicle with paid=true keeps every period that is already
--    COMPLETE today treated as settled (paid_through = last day of the last
--    complete billing period; for a vehicle whose end date has passed that is
--    its whole stay). When its allocation was booked inside the running period,
--    the days up to that booking stay settled too (they were paid with money).
--    The running period's remaining days and every later period become owed and
--    invoiceable. Nothing is back-billed for periods that are complete today. The
--    balance does not change: it has always been payment-based for vehicle rent.
--    Vehicles with paid=false get NULL (nothing settled).
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS paid_through DATE;

UPDATE vehicles v
   SET paid_through = GREATEST(
         date_trunc(CASE WHEN v.billing_period = 'yearly' THEN 'year' ELSE 'month' END,
                    CURRENT_DATE::timestamp)::date - 1,
         CASE WHEN v.end_date IS NOT NULL AND v.end_date < CURRENT_DATE THEN v.end_date END,
         (SELECT max(a.created_at)::date FROM payment_allocations a
           WHERE a.kind = 'vehicle' AND a.ref_id = v.id))
 WHERE v.paid AND v.paid_through IS NULL;

-- 2. The recurring master "bezahlt" flag no longer exists as a sticky flag (BIL-01).
--
--    recurring_charges.paid settled every period of an open-ended Nebenkosten
--    forever — including periods that had not happened yet — so they were never
--    invoiced and never owed. The master slider now writes explicit per-period
--    keys (paid_periods) for the periods complete at that moment, and the column
--    stays false; the "paid" value in the API is derived from the keys.
--
--    Existing rows: a charge with paid=true gets every period that is COMPLETE
--    today and not billed by an active invoice added to paid_periods as a whole-
--    period payment (replacing a fixed partial for that key), then paid=false.
--    Exactly those periods stay settled; the running period and all later periods
--    become owed and invoiceable. Invoiced periods were already excluded from the
--    flag's credit, so they keep being settled through their invoice. The startup
--    backfill (BackfillPeriodPayments) books the real Zahlungseingang for keys
--    that had none, as it does for every other whole-paid period.
WITH complete_keys AS (
    SELECT rc.id,
           array_agg(to_char(g, CASE WHEN rc.period = 'yearly' THEN 'YYYY' ELSE 'YYYY-MM' END)
                     ORDER BY g) AS ks
      FROM recurring_charges rc
     CROSS JOIN LATERAL generate_series(
           date_trunc(CASE WHEN rc.period = 'yearly' THEN 'year' ELSE 'month' END, rc.start_date::timestamp),
           CURRENT_DATE::timestamp,
           CASE WHEN rc.period = 'yearly' THEN interval '1 year' ELSE interval '1 month' END) AS g
     WHERE rc.paid
       AND rc.start_date <= CURRENT_DATE
       AND (rc.end_date IS NULL OR g::date <= rc.end_date)
       -- complete: the period's end (or the day after the end date) is not after today
       AND LEAST((g + CASE WHEN rc.period = 'yearly' THEN interval '1 year' ELSE interval '1 month' END)::date,
                 COALESCE(rc.end_date + 1, 'infinity'::date)) <= CURRENT_DATE
       AND NOT EXISTS (
           SELECT 1 FROM invoice_source s JOIN invoices i ON i.id = s.invoice_id
            WHERE s.kind = 'recurring' AND s.ref_id = rc.id AND NOT i.canceled
              AND s.period_key = to_char(g, CASE WHEN rc.period = 'yearly' THEN 'YYYY' ELSE 'YYYY-MM' END))
     GROUP BY rc.id
), targets AS (
    SELECT rc.id, COALESCE(k.ks, '{}'::text[]) AS ks
      FROM recurring_charges rc LEFT JOIN complete_keys k ON k.id = rc.id
     WHERE rc.paid
)
UPDATE recurring_charges rc
   SET paid_periods = ARRAY(SELECT DISTINCT x FROM unnest(rc.paid_periods || t.ks) AS x ORDER BY x),
       paid_fixed   = rc.paid_fixed - t.ks,
       paid         = false,
       updated_at   = now()
  FROM targets t
 WHERE t.id = rc.id;

-- 3. Optional Idempotency-Key for POST /charges and POST /persons/{id}/recurring
--    (WEB-02), scoped to the acting user like payments (070). Existing rows keep
--    NULL keys; requests without the header behave as before.
ALTER TABLE charges ADD COLUMN IF NOT EXISTS idempotency_actor BIGINT;
ALTER TABLE charges ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE charges ADD COLUMN IF NOT EXISTS request_fingerprint TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS uq_charges_actor_idempotency
    ON charges (COALESCE(idempotency_actor, 0), idempotency_key)
    WHERE idempotency_key IS NOT NULL;

ALTER TABLE recurring_charges ADD COLUMN IF NOT EXISTS idempotency_actor BIGINT;
ALTER TABLE recurring_charges ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE recurring_charges ADD COLUMN IF NOT EXISTS request_fingerprint TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS uq_recurring_charges_actor_idempotency
    ON recurring_charges (COALESCE(idempotency_actor, 0), idempotency_key)
    WHERE idempotency_key IS NOT NULL;
