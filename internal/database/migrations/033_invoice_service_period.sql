-- § 11 Abs 1 Z 4 UStG: an invoice must state the day/period of supply
-- (Leistungszeitraum). Store it on the immutable document, snapshotted at issue
-- from the billed positions' earliest date through the issue date.
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS leistung_from DATE;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS leistung_to   DATE;
