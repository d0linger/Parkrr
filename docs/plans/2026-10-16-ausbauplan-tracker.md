# Ausbauplan 16.10.2026: Quick Wins und Must-Haves

Quelle: Artifact „Parkrr Ausbauplan“ (50 Vorschläge). Umgesetzt werden Nr. 1 bis 36.
Kundenwirksame Automatiken (Mails, Mahnungen, Erinnerungen) kommen als Code hinter
einem Schalter, der standardmäßig AUS ist. Commits tragen `[Ausbau B<n>]` im Betreff.

Legende: `[x]` umgesetzt · `[~]` teilweise (Grund dahinter) · `[ ]` offen · `[>]` vertagt (Grund)

## B1 Technik und CI

- [x] 15 Docker-Image mit derselben Go-Version wie CI (`golang:1.26.9-alpine`, `scripts/check-go-version.cjs` in CI)
- [x] 16 Playwright-Browser in CI zwischenspeichern (`actions/cache`, Schlüssel = Playwright-Version)
- [x] 17 Live-Dashboard-Test in CI wirklich ausführen (Port 18099, Zugangsdaten, `PARKRR_E2E_REQUIRE_LIVE`)
- [x] 2 Wiederherstellungsprobe in der Betriebsbereitschaft (Punkt „Sicherung geprüft“, 14 Tage)
- [~] 18 Planer-Farben und Radien auf Tokens: 6 Regeln der Planfläche nutzen jetzt die vorhandenen `--gp*`-Tokens (ohne sichtbare Änderung). Offen: rund 140 weitere Literale sind eine eigene Planer-Palette (Fahrzeugkategorien, Zustände, Schatten) ohne Token in DESIGN.md; die Umstellung braucht zuerst eine Palettenentscheidung, sonst ändert sie Farben.

## B2 Bedienung

- [~] 3 Hallen-Belegung: jede Halle (auch leer) öffnet ihren Planer. Auslastung in Prozent bewusst nicht: Plätze entstehen im Planer erst beim Platzieren, eine Platzzahl wäre irreführend; Flächenauslastung folgt mit Nr. 38.
- [x] 7 Tastenkürzel-Übersicht auf „?“ (Tabelle `APP_SHORTCUTS`; `/` Suche, `N` neu; im Planer gilt dessen eigene Hilfe)
- [x] 4 Befehlspalette mit Aktionen (Person, Gefährt, Zusatzkosten, Kürzel, Hell/Dunkel; rollenabhängig)
- [x] 6 Kompakte Listenansicht (Gefährte, Personen; Schalter „Kompakt“, im Browser gemerkt)
- [x] 5 Gespeicherte Filteransichten (Gefährte, Personen; mitgelieferte und eigene, im Browser)

## B3 Betrieb

- [x] 13 Unvollständige Gefährte in der Heute-Liste (`GET /api/vehicles/incomplete`: ohne Maße, ohne Kennzeichen; Filter `?missing=`)
- [x] 8 Strom: neue Planer-Zone „Strom“ (markiert, nicht sperrend), da Stellplätze erst beim Platzieren entstehen. Ladebedarf mit Mitte außerhalb jeder Stromzone warnt im Planer und in der Heute-Liste, nur in Hallen mit Stromzonen. Gleiche Rechnung in `geometry.js` und Go.
- [x] 12 Etikettenbogen je Halle (`GET /api/halls/{id}/labels`, A4, zehn je Seite)
- [x] 11 Wochenliste als PDF (`GET /api/reports/week.pdf`, Abholungen, Einlagerungen, Abholwünsche; Knopf im Kalender)
- [x] 10 Kalender-Abo (`/ical/{token}`, Migration 081; nur Hash gespeichert, ohne Namen und Beträge, widerrufbar, Pfad im Zugriffsprotokoll geschwärzt)
- [x] 9 Gefährte per CSV importieren (`POST /api/import/vehicles` + Vorlage; gleiche Prüfung und Anlage wie das Formular; Fehlerzeilen im Dialog)
- [x] 14 Schnellpreis im Tarife-Reiter (rechnet mit `vehicleRentalTotal`; „Als Gefährt anlegen“, bei Beginn in der Zukunft als Reservierung)

## B4 Abrechnung und Mail

- [x] 1 Sammel-Erinnerung (`POST /api/invoices/remind`, je Rechnung derselbe Handler wie der Knopf; höchstens 10 je Aufruf, die Oberfläche schickt Zehnerpakete)
- [x] 36 Mail-Warteschlange (Migration 082, Lauf alle 30 s, Wiederholung 1 min bis 6 h; hängengebliebene Sendungen werden „Zustellung unklar“, nicht doppelt verschickt; Anonymisieren leert sie). Manuelle Erinnerungen bleiben bewusst synchron.
- [x] 19 Rechnung per Mail mit PDF (`POST /api/invoices/{id}/send`, Versandstand an der Rechnung; optional nach dem automatischen Lauf, Standard aus)
- [x] 20 Automatische Mahnstufen (Standard aus; Stufe erst bei Zustellung gebucht, unter derselben Sperre wie der Knopf; „nicht automatisch mahnen“ je Person)
- [x] 26 Vorschau Rechnungslauf (`GET /api/invoices/preview`, rein lesend mit derselben Positionsermittlung; keine Nummer, keine Sperre)

## B5 Saison und Kunden

- [ ] 21 Saisonwechsel als Sammelaktion
- [ ] 22 Kunden vor Vertragsende erinnern (Standard aus)
- [ ] 23 Abholung in Zeitfenstern
- [ ] 31 Warteliste

## B6 Halle

- [ ] 24 Zustandsfotos an der Übergabe, Vergleich bei der Abholung
- [ ] 25 Serviceaufgaben während der Einlagerung
- [ ] 33 Platzvorschlag beim Einlagern
- [ ] 34 Schlüssel- und Handsenderbuch

## B7 Geld und Recht

- [ ] 27 Kontoauszug importieren (camt.053), Zahlungen zuordnen
- [ ] 28 Datenauskunft je Person (Art. 15 DSGVO)
- [ ] 29 Wertsicherung nach Verbraucherpreisindex
- [ ] 32 Kautionen getrennt führen
- [ ] 30 Einstellvertrag als PDF, Annahme im Portal

## B8 Rollen

- [ ] 35 Zwei Rollen dazu: Buchhaltung und Hallendienst

## Danach

- [ ] Ruhezeit („Kurz weg“): Entscheidungen vom 16.10.2026 (Artifact „Parkrr Ruhezeit“):
  Platz je Ruhezeit wählbar, Standard „halten“; Haltegebühr 0 %; gekürzt ab dem ersten Tag;
  Pauschale bleibt, Ruhezeit wird nur vermerkt; vorab bezahlt: „bezahlt bis“ verschiebt sich;
  gebundene Nebenkosten ruhen mit; Kunden können die Ruhezeit im Portal anfragen.
