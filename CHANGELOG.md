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
- **Portal-Briefkasten:** Kunden können Kontaktdaten-Änderungen und Abholtermine
  einreichen; der Betreiber übernimmt sie per Klick auf der Übersicht — das
  Portal selbst schreibt nie in Stammdaten.
- **Portal auf Englisch:** folgt der Browsersprache, umschaltbar, mit Gedächtnis.
- **Kalenderansicht:** Abholungen, Reservierungen, fällige Rechnungen und
  Abholwünsche im Monatsraster.
- **Bulk-Aktionen:** mehrere Gefährte gemeinsam als abgeholt markieren oder
  stornieren.
- **Datei-Anhänge** (PDF/JPEG/PNG) je Person und Gefährt, mit erzwungenem
  Download und Prüfung am Dateiinhalt.
- **Verlauf je Person** (alle Ereignisse in einem Strom) und
  **Belegungshistorie je Stellplatz** ("wer stand wann auf Platz 3?").
- **Titelbild & Fotoreihenfolge** für Gefährt-Fotos; der Planer zeigt das
  Titelbild.
- **E2E-Tests für Login, 2FA und Passkey** (echter TOTP, virtueller
  WebAuthn-Authenticator).
- **Betreiber-Handbuch** (docs/betreiber-handbuch.md).

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

### Behoben (Mehr-Augen-Durchsicht des Programms)

Eine Durchsicht über alle 41 Programm-Commits, aus elf unabhängigen Blickwinkeln,
hat die folgenden Fehler gefunden. Jeder wurde am Code nachgeprüft, bevor er
angefasst wurde.

- **Aussperr-Sperre beim Sperren von Konten:** Der Schutz vor „null Admins"
  zählte auch bereits gesperrte Admins mit. Damit ließ sich ein Konto nach dem
  anderen sperren, bis sich niemand mehr anmelden konnte — und das Entsperren
  liegt selbst hinter der Admin-Rolle. Gezählt werden jetzt nur anmeldbare
  Admins; der erzwungene Bootstrap-Lauf
  (`PARKRR_ADMIN_PASSWORD_FORCE`) hebt zusätzlich eine Sperre auf und ist damit
  der dokumentierte Notausgang.
- **Kundenwunsch konnte eine DSGVO-Löschung rückgängig machen:** Ein vor der
  Löschung eingereichter Kontaktdaten-Wunsch schrieb beim „Übernehmen" E-Mail,
  Telefon und Anschrift wieder auf die anonymisierte Person. Wird jetzt
  abgelehnt (ablehnen/erledigen bleibt möglich).
- **Die Löschung erreicht jetzt alle Nebentabellen:** Portal-Wünsche (die
  gewünschte neue Anschrift stand dort im Klartext), Datei-Anhänge, das
  Versandprotokoll und die Stellplatz-Historie (Kennzeichen + Personenbezug)
  blieben unberührt — drei davon hängen an keinem Fremdschlüssel und überlebten
  auch ein echtes Löschen.
- **Passkey-Anmeldung ignorierte die Konto-Sperre:** Ein gesperrtes Konto konnte
  sich per Passkey anmelden und bekam eine neue Sitzung samt „angemeldet"-Eintrag
  im Protokoll.
- **Automatischer Rechnungslauf hielt bei EINEM lückenhaften Datensatz an:** Eine
  fehlende Empfängeranschrift (ab 400 € Pflicht) galt als betriebsweiter Mangel
  und brach den ganzen Lauf ab — alle nachfolgenden Personen blieben unfakturiert,
  Nacht für Nacht, ohne Hinweis in der Zusammenfassung. Verkäufer- und
  Empfängermängel werden jetzt unterschieden; die Zusammenfassung nennt
  übersprungene Personen und einen Abbruch ausdrücklich.
- **Ein Programmfehler im Rechnungslauf konnte den Dienst beenden:** Der Lauf
  umging die Absturzsicherung, die bei jedem normalen Aufruf greift.
- **Planer: Torhöhe und Traglast wurden nach dem Ablegen nicht mehr geprüft.**
  Tarif-Standardmaße galten nur in der Ablageleiste; auf einem Platz meldete
  dasselbe Gefährt keine Maße, und die Warnung blieb still.
- **Planer: Fläche und Raumangaben blieben nach Wandänderungen und Rückgängig
  stehen** (auch im Export) — bis zufällig eine andere Aktion neu rechnete.
- **Ein neu hochgeladenes Foto verdrängte das gewählte Titelbild.**
- **Mehrfachauswahl: der Zähler blieb bei „0 ausgewählt"**, obwohl die Auswahl
  griff — eine Massenaktion ohne prüfbare Zahl.
- **Kalender: die Ebene „Fällige Rechnung" konnte nur Vergangenes zeigen** und war
  in jedem künftigen Monat garantiert leer.
- **Revisionsexport brach bei großem Protokoll still nach zehn Sekunden ab** —
  mit bereits gesendetem Status 200.
- **Widerrufene Portal-Links verschwanden binnen einer Stunde** statt der
  zugesagten 30 Tage; der Betreiber sah nicht mehr, dass je einer bestand.
- **500er-Fehler wurden ohne Ursache protokolliert:** elf Stellen reichten die
  falsche Fehlervariable weiter. Ein Quelltext-Test verhindert den Rückfall.
- **Diagramme: Tastatur-Ansage und Datentabelle kamen bei Screenreadern nie an**
  (beide lagen innerhalb einer als Bild ausgezeichneten Hülle).
- **Escape wirkte nach einem Planer-Dialog sitzungsweit nicht mehr**, wenn der
  Dialog durch einen Seitenwechsel verschwand.
- **Abmelden räumt die Ansichtszustände auf:** der nächste Anmeldende am selben
  Rechner sah sonst die Suchbegriffe und Filter des vorigen.
- **Das Versandprotokoll nennt die tatsächlich angeschriebenen Adressen**, nicht
  die Rohliste (unparsbare werden vom Versand verworfen).
- **Personen-Verlauf zeigte Datumsangaben um einen Tag versetzt**, sobald eine
  Geschäftszeitzone gesetzt ist.
- **Audit-Suche:** `%` und `_` wirkten als Jokerzeichen; eine Ein-Zeichen-Eingabe
  lieferte das gesamte Protokoll als „Treffer".
- **Portal-Briefkasten:** der Deckel gegen Spam ließ sich durch gleichzeitige
  Einreichungen umgehen.
- **„Protokoll löschen"** wurde Bearbeitern angeboten, obwohl nur Admins es
  dürfen (Fehlschlag erst nach dem Rückgängig-Fenster).
- Kleinere Korrekturen: Kunden- und Betreiberansicht nennen denselben Zustand
  jetzt gleich („eingelagert"), das Portal-Wunsch-Protokoll zeigt wieder Werte
  statt „leer → leer", PDF-Schriften werden einmal statt je Dokument geladen, und
  der Portal-Kennzahlenpfad lädt nicht mehr die Abgleichsdaten aller Kunden.

### Behoben (zweite Durchsicht)

- **Der automatische Rechnungslauf überlebt einen Neustart mittendrin:** Der Lauf
  merkte sich „erledigt", bevor er begann. Ein Update, ein Reboot oder ein
  Speicherengpass in genau dieser Minute ließ den Rest des Laufs ersatzlos
  ausfallen — bis zum nächsten Termin, also je nach Zeitplan einen Monat lang,
  ohne Eintrag und ohne Alarm. Der Merker wird jetzt erst nach dem Lauf gesetzt;
  eine Wiederholung kostet nichts, weil abgerechnete Perioden gesperrt bleiben.
- **Ein Lauf, der an der Zeitgrenze abbricht, sagt das jetzt auch:** Bisher las
  sich die Zusammenfassung wie ein geglückter Lauf, während Hunderte Personen
  unfakturiert blieben.
- **Mahnen blockiert die Anwendung nicht mehr:** Der Mahnvorgang belegte während
  der E-Mail-Zustellung eine Datenbankverbindung und forderte für den
  Protokolleintrag noch eine zweite an. Bei mehreren gleichzeitigen Mahnungen und
  einem trägen Mailserver stand die ganze Anwendung. Außerdem geht eine versandte
  Mahnung nicht mehr verloren, wenn der Browser währenddessen geschlossen wird —
  sonst bekam der Kunde dieselbe Stufe ein zweites Mal.
- **Die Mahnstufe auf der Rechnungsseite stimmt nach dem Senden:** Ein zweiter
  Klick fragte weiter nach der „Zahlungserinnerung", während tatsächlich die
  1. Mahnung hinausging.
- **Filter überleben eine Massenaktion:** Status-, Personen- und Archivfilter der
  Gefährteliste wurden bei jedem Neuaufbau der Seite zurückgesetzt — auch direkt
  nach einer Massenaktion, was die fehlgeschlagenen Zeilen wieder aus der Auswahl
  warf. Und ein Tastendruck im Suchfeld löscht die Mehrfachauswahl nicht mehr.
- **Zu große Dateien sagen jetzt, dass sie zu groß sind,** statt „HTTP 413": die
  Größenschranke antwortet im Klartext, und die Anhang-Auswahl prüft schon vor
  dem Hochladen. **Achtung, echte Einschränkung:** die Obergrenze für einen
  Anhang liegt damit bei **8 MB statt bisher 10** — die 10 waren ohnehin nie
  erreichbar, weil der allgemeine Deckel für eine Anfrage darunter lag und ein
  größerer Anhang schon vorher unverständlich abgewiesen wurde. Ein Scan
  zwischen 8 und 10 MB muss künftig verkleinert werden.
- **Widerrufene Portal-Links bleiben als „widerrufen" sichtbar** — das Ausstellen
  eines neuen Links für irgendwen löschte zuvor die widerrufenen Zeilen aller
  Personen.
- **Der Start bricht nicht mehr an Zeilenenden ab:** Die Prüfsumme der
  Migrationen rechnet zeilenenden-unabhängig und hebt eine alt aufgezeichnete
  Summe einmalig an, statt sie bei jedem Start neu zu schreiben.
- **Die Löschung einer Person trifft im Mail-Protokoll genau ihre Adresse.**
  Zuvor wurde als Textmuster gesucht: eine Person mit der Adresse „%@%" — im
  Portal selbst wünschbar — hätte beim Löschen die Empfängerspalte des GANZEN
  Protokolls geleert, und fremde Zeilen mit ähnlicher Adresse wurden mit
  anonymisiert. Umgekehrt bleibt eine Adresse mit Leerraum jetzt nicht mehr
  stehen, während die Person als gelöscht gemeldet wurde.
- **Eine versandte Mahnung, die nicht festgehalten werden kann, wird gemeldet.**
  Bisher meldete die Oberfläche Erfolg, während die Stufe ungezählt blieb — der
  nächste Klick schickte dieselbe Stufe ein zweites Mal. Jetzt sagt die Meldung,
  dass die Mail draußen ist, und warnt ausdrücklich vor dem zweiten Klick.
- **Parkfläche, Raum-m² und Frei/Belegt stimmen nach jeder Planänderung.** Nach
  dem Verschieben an eine Wand oder dem Löschen von Wänden mit dem Auswahlrahmen
  zeigten die Kennzahlen und der SVG-/PDF-Export weiter den vorherigen Stand.
- **Die Neustart-Warnung der Betriebsüberwachung kann erstmals auslösen:** die
  Prozess-Kennzahlen, auf die die mitgelieferte Alarmregel sich stützt, wurden
  bisher gar nicht ausgeliefert — eine stille Lücke, die wie „alles ruhig" aussah.
- **Rücksicherung und Unterschrift nennen jetzt erreichbare Grenzen.** Beide
  versprachen eine Größe, die nie durchging: bei der Rücksicherung lag die eigene
  Grenze exakt auf dem allgemeinen Deckel für eine Anfrage, sodass der Umschlag
  der Übertragung immer darüber lag — die hilfreiche Meldung („dafür bitte die
  Kommandozeile, `parkrr restore`") wurde nie erreicht. Bei der Unterschrift
  scheiterte ein großes Bild am JSON-Leser mit einem Syntaxfehler, statt
  „Signatur zu groß" zu melden. **Achtung, echte Einschränkung:** die
  Rücksicherung über den Browser liegt jetzt bei **8 MB statt nominell 9** —
  größere Sicherungen gehen weiterhin über die Kommandozeile.

### Entfernt
- Die seit Migration 012 tote Alt-Tabelle `flatrate_paid_years`; noch vorhandene
  historische Zeilen werden beim Update automatisch ins Änderungsprotokoll
  archiviert.

### Betrieb
- Neue optionale Umgebungsvariablen: `PARKRR_ALERT_EMAIL`, `PARKRR_TIMEZONE`,
  `PARKRR_REQUIRE_2FA`, `PARKRR_PASSKEY_ONLY` — alle Default aus/leer, ohne sie
  ändert sich nichts. Details in der README (Configuration).
- Abgelaufene Portal-Links und Passkey-Zeremonien werden jetzt stündlich
  aufgeräumt, nicht mehr nur beiläufig.
- Backups: fehlgeschlagene geplante Läufe können alarmieren (s. o.); die
  Aufbewahrung kann eine Mindest-Historie in Tagen garantieren.
