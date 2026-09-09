-- Mahn-Gedächtnis (Hundert 15). Das Erinnern war zustandslos: jeder Klick
-- verschickte denselben freundlichen Text, und ob eine Rechnung schon dreimal
-- gemahnt wurde, stand nirgends — das Audit-Log kennt zwar die remind-Aktion,
-- aber dessen Kurzfenster (365 Tage) darf kein Mahnverlauf sein.
--
-- Jede Zeile ist EIN Versand: Stufe, Empfänger, Zeitpunkt. Die Stufe entsteht
-- beim Senden aus der Zahl der vorigen Versendungen (1 = Zahlungserinnerung,
-- 2 = 1. Mahnung, 3 = 2./letzte Mahnung — Stufe 3 wiederholt sich).
--
-- ON DELETE CASCADE: Rechnungen sind per Trigger ohnehin unlöschbar (BAO §132);
-- die Kaskade greift nur im Test-Teardown über die purge-Ausnahme.
CREATE TABLE IF NOT EXISTS invoice_reminders (
    id         BIGSERIAL PRIMARY KEY,
    invoice_id BIGINT NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    level      INT    NOT NULL CHECK (level BETWEEN 1 AND 3),
    sent_to    TEXT   NOT NULL DEFAULT '',
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_invoice_reminders_invoice ON invoice_reminders (invoice_id, sent_at DESC);
