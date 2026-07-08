-- 015: per-period payment tracking for flat-rate agreements (Pauschalen).
-- Each row marks one sub-period as paid: a calendar month ("YYYY-MM") for
-- monthly agreements, a year ("YYYY") for yearly ones. This layers on top of the
-- legacy whole-agreement `paid` flag WITHOUT changing existing billing: a
-- sub-period counts as paid when the master `paid` flag is set OR a matching row
-- exists here. Existing agreements have no rows, so their `paid` flag alone
-- still decides and all figures stay exactly as before.
CREATE TABLE IF NOT EXISTS flat_rate_period_payments (
    period_id  BIGINT      NOT NULL REFERENCES flat_rate_periods(id) ON DELETE CASCADE,
    period_key TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (period_id, period_key)
);

CREATE INDEX IF NOT EXISTS idx_frpp_period ON flat_rate_period_payments(period_id);
