-- E-Mail-Versandprotokoll (Hundert 86). Ob eine Mahnung oder ein Portal-Link je
-- RAUSGING, stand bisher nur verstreut: Mahnungen seit 058 in invoice_reminders,
-- Portal-Links als Audit-Zeile, Backup-Alarme nur im Log. "Hat der Kunde die
-- Mahnung bekommen?" braucht EINE Stelle mit Empfänger, Betreff, Ausgang.
--
-- Bewusst OHNE Mail-Inhalt: der Body kann personenbezogene Details tragen und
-- ist aus Betreff + Kontext rekonstruierbar. Fehltexte (error) sind
-- SMTP-Diagnosen, keine Inhalte.
CREATE TABLE IF NOT EXISTS mail_log (
    id         BIGSERIAL PRIMARY KEY,
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    recipients TEXT NOT NULL,
    subject    TEXT NOT NULL,
    ok         BOOLEAN NOT NULL,
    error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_mail_log_sent ON mail_log (sent_at DESC);
