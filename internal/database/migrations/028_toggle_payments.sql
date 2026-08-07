-- P2.3: the per-item "bezahlt" slider now moves money. Toggling a position paid
-- records a linked "auto" payment for its open amount; toggling it open removes
-- that payment. The flag marks those payments so un-toggling deletes exactly them
-- and never a manually recorded payment.

ALTER TABLE payments ADD COLUMN IF NOT EXISTS auto BOOLEAN NOT NULL DEFAULT FALSE;
