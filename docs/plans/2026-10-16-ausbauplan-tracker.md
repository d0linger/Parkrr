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

- [ ] 3 Hallen-Belegung anklickbar, mit Auslastung und leeren Hallen
- [ ] 7 Tastenkürzel-Übersicht auf „?“
- [ ] 4 Befehlspalette mit Aktionen
- [ ] 6 Kompakte Listenansicht
- [ ] 5 Gespeicherte Filteransichten

## B3 Betrieb

- [ ] 13 Unvollständige Gefährte in der Heute-Liste
- [ ] 8 Stromanschluss am Stellplatz, Abgleich mit dem Ladebedarf
- [ ] 12 Etikettenbogen je Halle
- [ ] 11 Wochenliste der Abholungen als PDF
- [ ] 10 Kalender-Abo für Termine
- [ ] 9 Gefährte per CSV importieren
- [ ] 14 Schnellpreis für Anfragen

## B4 Abrechnung und Mail

- [ ] 1 Sammel-Erinnerung für überfällige Rechnungen
- [ ] 36 Mail-Warteschlange mit Wiederholung
- [ ] 19 Rechnungsversand per Mail mit PDF
- [ ] 20 Automatische Mahnstufen (Standard aus)
- [ ] 26 Vorschau für den Rechnungslauf

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

- [ ] Ruhezeit („Kurz weg“): Vorschlag im Artifact „Parkrr Ruhezeit“, Umsetzung nach den Entscheidungen dort
