-- Merker des automatischen Rechnungslaufs (Hundert 16).
--
-- Der Lauf hielt seinen letzten Zeitpunkt NUR im Speicher und setzte ihn beim
-- Start auf "jetzt". Ein Neustart, der ueber die geplante Minute fiel — ein
-- Image-Update, ein OOM-Kill, ein Host-Reboot um 06:00:30 bei Cron "0 6 1 * *" —
-- verschob den naechsten Termin damit auf den naechsten Monat: die Fakturierung
-- des laufenden fiel lautlos aus. Kein Protokolleintrag, kein Alarm, denn
-- runAutoInvoice wurde nie betreten.
--
-- backup_status loest dasselbe Problem fuer die Sicherungen seit jeher ueber eine
-- Zeile in der Datenbank; der Rechnungslauf bekommt hier seine.
--
-- Nachholen ist unbedenklich: die Periodensperre (invoice_source) macht den Lauf
-- idempotent — eine bereits abgerechnete Periode liefert keine Positionen, der
-- nachgeholte Lauf ist dann schlicht leer.
CREATE TABLE IF NOT EXISTS auto_invoice_status (
    id          SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_run_at TIMESTAMPTZ
);

INSERT INTO auto_invoice_status (id, last_run_at) VALUES (1, NULL)
ON CONFLICT (id) DO NOTHING;
