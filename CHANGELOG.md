# Changelog

Alle nennenswerten Änderungen an Parkrr, aus Sicht der Menschen, die es bedienen
und betreiben. Format angelehnt an [Keep a Changelog](https://keepachangelog.com/de/);
das Projekt lebt auf `dev` und wird über Pull Requests nach `main` (= Produktion)
gebracht — „Unreleased" ist, was auf `dev` liegt und den nächsten Deploy bildet.

Pflege-Regel: Jeder Batch/PR, der Verhalten ändert, trägt hier eine Zeile ein —
in der Sprache der Anwender („Rechnungen lassen sich als CSV exportieren"), nicht
der Implementierung („neue Spalte in export.go").

## [Unreleased]

Stand des Verbesserungsprogramms „Parkrr-Hundert" (September 2026).

### Hinzugefügt
- **Konto sperren statt löschen:** Benutzerkonten können deaktiviert werden;
  die Sperre wirkt sofort (auch auf laufende Sitzungen) und erhält die
  Urheberschaft auf Rechnungen, Zahlungen und Übergabeprotokollen.
- **Mahnwesen mit Stufen:** Zahlungserinnerung → 1. Mahnung → 2./letzte Mahnung,
  mit Gedächtnis („2× gemahnt, zuletzt …") an der Rechnung.
- **Offene-Posten-Liste als PDF** mit Stichtag, neben den CSV-Exporten.
- **CSV-Export für Rechnungen und Zusatzkosten** (Buchhaltung/Steuerberater).
- **Revisionssicherer Audit-Export:** JSONL mit SHA-256-Hashkette, prüfbar ohne
  Parkrr (Audit-Seite → „Revisionsexport").
- **Alarm bei Backup-Fehlschlag** per E-Mail (`PARKRR_ALERT_EMAIL`).
- **Mindest-Aufbewahrung für Backups** in Tagen, zusätzlich zur Anzahl
  (Backup-Reiter) — ein Boden, der Historie schützt.
- **2FA-Pflicht** (`PARKRR_REQUIRE_2FA`) und **Passkey-only-Modus**
  (`PARKRR_PASSKEY_ONLY`) als Betreiber-Schalter, Default aus.
- **Geschäftszeitzone** (`PARKRR_TIMEZONE`): „heute" und „dieser Monat" folgen
  dem Betrieb, nicht der Container-Uhr.
- **Standardmaße je Tarif** für den Garagenplaner: ungemessene Gefährte erben
  eine brauchbare erste Näherung; eigene Maße gewinnen immer.
- **Batch-Speichern im Garagenplaner:** Auto-Anordnen schreibt alle Plätze in
  einer Transaktion — keine halb angeordnete Halle mehr bei einem Abbruch.
- **Automatischer Rechnungslauf** (`PARKRR_AUTO_INVOICE_CRON`, Default aus):
  erstellt Rechnungen nach Zeitplan über denselben Pfad wie der Knopf; schon
  abgerechnete Perioden werden nie doppelt fakturiert.
- **`parkrr seed-demo`:** befüllt eine frische Datenbank mit erkennbaren
  Demo-Daten (idempotent; weigert sich auf einer benutzten Datenbank).
- **Storno-Dokumente nennen die stornierte Rechnungsnummer** (§11 UStG).

### Geändert
- **PDFs (Rechnung, Übergabeprotokoll, Berichte) mit eingebetteter
  Unicode-Schrift:** Kundennamen wie „Łukasz" oder „Nováková" stehen jetzt
  richtig auf den Belegen.
- **DSGVO-Anonymisierung reicht bis zur Unterschrift** im Übergabeprotokoll;
  der Beleg selbst bleibt als Beleg erhalten.
- **Abgeschnittene Listen sagen es:** zeigt der Server nur die ersten 1000
  Einträge, steht das jetzt über der Liste, statt vollständig auszusehen.
- Schnelle Routenwechsel können die neue Seite nicht mehr mit dem Ergebnis der
  alten überschreiben (Render-Wettlauf behoben).
- Formulardialoge behalten bei einem Fehler die Eingaben, statt zu schließen
  (u. a. S3-Wiederherstellung, Recovery-Codes).
- Die Symbol-Knöpfe (✕, 🗑, ⟳, ⭳) sind echte Icons statt Emojis — einheitlich
  auf allen Plattformen und für Screenreader benannt.

### Betrieb
- Neue optionale Umgebungsvariablen: `PARKRR_ALERT_EMAIL`, `PARKRR_TIMEZONE`,
  `PARKRR_REQUIRE_2FA`, `PARKRR_PASSKEY_ONLY` — alle Default aus/leer, ohne sie
  ändert sich nichts. Details in der README (Configuration).
- Abgelaufene Portal-Links und Passkey-Zeremonien werden jetzt stündlich
  aufgeräumt, nicht mehr nur beiläufig.
- Backups: fehlgeschlagene geplante Läufe können alarmieren (s. o.); die
  Aufbewahrung kann eine Mindest-Historie in Tagen garantieren.
