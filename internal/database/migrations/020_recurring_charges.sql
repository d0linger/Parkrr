-- Recurring extra costs ("Wiederkehrende Nebenkosten"): a person-level cost that
-- accrues per period (monthly/yearly) like rent and is paid per period, on top of
-- the flat-rate / per-vehicle rent. Per-period payment state is kept inline
-- (whole-period keys + fixed partials) mirroring the flat-rate agreements.
CREATE TABLE IF NOT EXISTS recurring_charges (
    id           BIGSERIAL PRIMARY KEY,
    person_id    BIGINT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    description  TEXT NOT NULL,
    amount       NUMERIC(12,2) NOT NULL CHECK (amount >= 0),
    period       TEXT NOT NULL DEFAULT 'monthly' CHECK (period IN ('monthly','yearly')),
    start_date   DATE NOT NULL,
    end_date     DATE,
    paid         BOOLEAN NOT NULL DEFAULT false,
    paid_periods TEXT[] NOT NULL DEFAULT '{}',
    paid_fixed   JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_recurring_charges_person ON recurring_charges(person_id);
