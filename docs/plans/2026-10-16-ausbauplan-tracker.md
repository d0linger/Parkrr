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
- [x] 18 Planer-Farben und Radien auf Tokens: 6 Regeln der Planfläche nutzen die vorhandenen `--gp*`-Tokens. Nachtrag O2: die übrigen 155 festen Farbwerte der Planer-Regeln stehen als 107 Tokens `--gpc-*` mit ihrem bisherigen Wert (Pixel- und Computed-Style-Vergleich gleich), Palette in DESIGN.md.

## B2 Bedienung

- [x] 3 Hallen-Belegung: jede Halle (auch leer) öffnet ihren Planer. Nachtrag O1: Auslastung als Fläche (belegte m² / nutzbare Fläche), dieselbe Zahl in Startseite, Planer-Kopfzeile und Kennzahlen; die nutzbare Fläche rechnet nur der Planer und legt sie als `geometry.park` ab.
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

- [x] 21 Saisonwechsel (Dialog in der Gefährte-Liste; je Gefährt über „Erneut einstellen“, neu: aktueller Tarif statt Vorjahrespreis und Reservierung ab Beginn, beides optional). Nachtrag O3: „Platz vorschlagen“ nennt den Vorjahresplatz zuerst (Migration 088, `vehicles.copied_from`).
- [x] 22 Vertragsende-Erinnerung per Mail (Standard aus, Tage in der Mail-Automatik, je Gefährt und Ende genau einmal) und Verlängerungswunsch im Portal, „Übernehmen“ speichert das Gefährt über den normalen Weg
- [x] 23 Abholzeitfenster (Einstellungen), im Portal Auswahl freier Zeitpunkte; der letzte Platz wird unter Sperre nur einmal vergeben; Uhrzeit in Kalender, Wochenliste und Abo
- [x] 31 Warteliste (eigene Seite, Einwilligung Pflicht, „Übernehmen“ legt die Person an und öffnet das Gefährt-Formular, Hinweis in der Heute-Liste, Löschung nach 12 Monaten)

## B6 Halle

- [x] 24 Zustandsfotos an der Übergabe (Migration 084): feste Ansichten, Fotos hängen am Protokoll statt in der Galerie, bis 24 h nachreichbar, einzeln nicht löschbar; Schäden als Liste; bei der Abholung stehen Fotos und Schäden der Einlagerung daneben, das PDF hat einen Kontaktbogen im Vergleich
- [x] 25 Serviceaufgaben (Seite „Serviceaufgaben“): je Gefährt, Tarif, alle eingelagerten oder alle mit Ladebedarf, fällig nach Intervall; „Erledigt“ mit einem Tipp, mit Leistung auf Wunsch als Position über CreateCharge (Rückgängig nimmt beides zurück); Hinweis in der Heute-Liste; Ladebedarf ohne Aufgabe schlägt „Erhaltungsladung prüfen“ vor
- [x] 33 Platzvorschlag im Planer („Platz vorschlagen“ beim bereiten Gefährt): drei verschiedene Plätze, geprüft mit derselben Prüfung wie das Ziehen, bewertet nach Strom, Abholtermin (nah am Tor) und dichtem Packen; mit Fahrweg-Prüfung, wenn Tore oder Fahrstraßen gezeichnet sind
- [x] 34 Schlüssel- und Handsenderbuch (Seite „Schlüsselbuch“): Art und Nummer, nie ein Code; Ausgabe und Rückgabe mit Verlauf; die Übergabe zeigt Offenes und nimmt zurück; eine Person mit offenem Zugangsmittel lässt sich nicht löschen

## B7 Geld und Recht

- [x] 27 Kontoauszug (Seite „Kontoauszug“, Migration 085): camt.053 einlesen (nur gebuchte Gutschriften, kein doppelter Import über die Buchungsreferenz); Vorschlag über die Rechnungsnummer im Verwendungszweck, sonst Betrag und Name (bei Mehrdeutigkeit keiner); gebucht wird erst beim Zuordnen, über pay-invoices
- [x] 28 Datenauskunft (Art. 15): ZIP mit allen Tabellen mit Personenbezug, Rechnungs- und Übergabe-PDFs, Fotos, Anhängen und Mail-Protokoll; nur Admins, im Änderungsprotokoll; ein Test verlangt jede Tabelle mit person_id im Paket
- [x] 29 Wertsicherung (Tarife → Wertsicherung): VPI-Werte selbst eintragen, je Tarif Basismonat und Schwelle, Vorschau alt → neu je Gefährt, Anpassen ab Beginn der nächsten Periode; die Abrechnung teilt Perioden jetzt an Preisänderungen (models.sliceCents, ohne Änderungen centgenau wie vorher); Mitteilung per Mail nur auf Wunsch. Pauschalen bleiben fest (Entscheidung O4), in der Vorschau als „nicht angepasst“ genannt
- [x] 32 Kautionen (Personenseite): eigenes Konto, erhalten und zurückgezahlt sind kein Erlös; Einbehalt nur mit Grund, gebucht über pay-invoices; die Übergabe bei der Abholung erinnert an die offene Kaution
- [x] 30 Einstellvertrag (Einstellungen): versionierte Vorlage mit Platzhaltern, PDF je Gefährt; im Kundenzugang erst lesen, dann annehmen (genau die gelesene Version); das angenommene PDF wird Anhang der Person

## B8 Rollen

- [x] 35 Rollen Buchhaltung und Hallendienst (Migration 086): Buchhaltung liest alles, dazu Steuerbereich und Exporte, schreibt sonst nichts; Hallendienst platziert, übergibt, fotografiert, erledigt Service und gibt Schlüssel aus, sieht keine Beträge (403 auf Geld-Routen, Beträge in Gefährte-, Tarif-, Dienst- und Servicelisten geschwärzt), eigene Startseite „Heute in der Halle“. Ein Go-Test prüft jede registrierte Route für jede Rolle

## Danach

- [x] Ruhezeit („Kurz weg“), Migration 087: Knopf „Kurz weg …“ am Gefährt (1/2/3 Wochen, Datum oder offen; Platz halten oder freigeben; Vorschau aus der Abrechnung), danach „Ist zurück“, „+1 Woche“, „Doch abgeholt“. Miete und gebundene Nebenkosten ruhen tagesgenau (models.Pause in sliceCents); eine offene Ruhezeit ruht, bis jemand „Ist zurück“ tippt; nie in abgerechneten Monaten (409 mit frühestem Tag); vorab bezahlt verschiebt „bezahlt bis“; Heute-Liste, Kalender-Abo, Zeitleiste, Planer (schraffiert), Portal-Anfrage. Entscheidungen vom 16.10.2026 (Artifact „Parkrr Ruhezeit“):
  Platz je Ruhezeit wählbar, Standard „halten“; Haltegebühr 0 %; gekürzt ab dem ersten Tag;
  Pauschale bleibt, Ruhezeit wird nur vermerkt; vorab bezahlt: „bezahlt bis“ verschiebt sich;
  gebundene Nebenkosten ruhen mit; Kunden können die Ruhezeit im Portal anfragen.

## Offene Punkte (Entscheidungen vom 22.10.2026)

Alle acht nach der Empfehlung im Artifact „Parkrr Ausbauplan“.

- [x] O1 Hallen-Auslastung: Flächenauslastung wie im Planer (siehe Nr. 3). Hallen ohne gespeicherte Fläche melden sie beim ersten Öffnen durch einen Bearbeiter nach (`PUT /api/halls/{id}/park-area`, schreibt nur diesen Schlüssel).
- [x] O2 Planer-Farben: Tokens mit den heutigen Werten (siehe Nr. 18).
- [x] O3 Vorjahresplatz: erster Platzvorschlag, wenn der alte Platz in der Halle noch steht, leer ist und das Gefährt dort gültig steht; ein Tipp belegt genau diesen Platz. Frühere Kopien bekommen ihren Vorgänger aus dem Änderungsprotokoll.
- [x] O4 Pauschalen in der Wertsicherung: bleiben fest, keine Änderung.
- [x] O5 Ruhezeit, Platz vorab freigeben: bleibt, wie es ist.
- [x] O6 Hallendienst: darf „Ist zurück“ (bis heute; „bezahlt bis“ sieht er nicht). Dabei behoben: das Speichern des Hallendiensts im Planer scheiterte am Grundriss (403), bevor ein Platz geschrieben war.
- [x] O7 Live-Tests: vor dem Push lokal gegen eine Wegwerf-Instanz (eigene Datenbank, Port 18099) gelaufen.
- [x] O8 Einführung in zwei Schritten: Checkliste in `docs/plans/2026-10-22-einfuehrung-zwei-schritte.md`.
- [x] N1 Betreiber-Handbuch (Wahl vom 23.10.2026: je Bereich ein Abschnitt): neue Kapitel 10 (Rollen, Halle und Betrieb, Saison und Kunden, Geld und Recht, Ruhezeit, Mail-Automatik) und 11 (Einführung in zwei Schritten), Ergänzungen in §4, §5, §6 und §8.
