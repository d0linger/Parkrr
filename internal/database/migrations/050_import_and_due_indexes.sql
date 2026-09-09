-- Zwei Lese-Indizes (Hundert B3, Findings API-26 und API-27). Beide rein additiv,
-- keine Daten- oder Verhaltensänderung.

-- Der CSV-Import prüft je Zeile lower(email) auf Dubletten; das war pro Zeile ein
-- Seq-Scan über persons. BEWUSST NICHT UNIQUE: Bestandsinstallationen können legitime
-- doppelte E-Mails halten, und ein Unique-Index würde deren Start-Migration scheitern
-- lassen — Eindeutigkeit erzwingen ist eine separate, betreibergeführte Bereinigung.
CREATE INDEX IF NOT EXISTS idx_persons_email_lower ON persons (lower(email));

-- OverdueInvoices filtert NOT canceled AND cancels_id IS NULL AND due_on IS NOT NULL
-- AND due_on < CURRENT_DATE; der einzige bestehende Index ist (person_id, issued_on).
-- Partiell auf die konstanten Prädikate der Abfrage, Schlüssel ist due_on.
CREATE INDEX IF NOT EXISTS idx_invoices_due_on
    ON invoices (due_on) WHERE NOT canceled AND cancels_id IS NULL;
