# Seitenweite UI-Verfeinerung – 12. September 2026

Nachtrag vom 13. September: Die verbleibenden Teile der eigenständigen
Dialogvorlage sind [in die echte Anwendung integriert](charge-integration-20260913.md).
Die lokale App auf Port 8099 wurde anschließend aktualisiert. Der nachfolgende
Bericht dokumentiert weiterhin den ursprünglichen Durchgang vom 12. September.

## Umfang

Fortsetzung der vorhandenen [Seitenüberarbeitung](page-by-page-overhaul-20260910.md),
direkt in der Anwendung, kein austauschbares Mockup. Impeccable wurde für
Aufgabenstruktur, Gruppierung, progressive Offenlegung und responsive Prüfung
verwendet. Fahrwerk-Farben, lokale Schriftarten, native Bedienelemente und die
strikte CSP bleiben erhalten; keine neue Bibliothek und kein CDN.

Die vorherige Überarbeitung umfasst bereits alle 17 internen Ansichten sowie
Anmeldung und Kundenportal. Dieser Durchgang ergänzt insbesondere die noch
einspaltigen Dialoge und die sekundären Formulare. Bewährte Ansichten werden
nicht allein für einen größeren Diff neu gezeichnet.

| Seite / Ablauf | Ergebnis dieses Durchgangs |
| --- | --- |
| Übersicht | Bestehende Geld-/Bestandsgruppen, Warnungen und parallele Datenabfragen beibehalten und erneut geprüft. |
| Personen | Suche/Sortierung gemeinsam; Name und Kontakt paarweise. Adresse/Notizen bei Neuanlage eingeklappt, vorhandene Angaben beim Bearbeiten sichtbar. |
| Personendetail | Kompaktere Abschnittsabstände; Pauschal-, Zahlungs- und Kostenformulare verwenden das gemeinsame Feldraster. Gefährt-Verwaltung einer Pauschale bleibt sichtbar. |
| Gefährte | Gemeinsame Such-/Sortierzeile; Stammdaten, Datumsbereich und Zeitraum/Preis paarweise. Notizen, Strombedarf und Planersymbol separat aufklappbar. |
| Gefährtdetail | Bestehende Übersicht und Status-/Zahlungsaktionen erhalten; Bearbeitung nutzt das neue Formularsystem. |
| Zusatzkosten | Ein kombiniertes Katalog-/Freitextfeld statt zweier Eingaben, Live-Betragsvorschau, Datumskurzwege, optionale Gefährt-Zuordnung. Betrag/Menge auch mobil nebeneinander. |
| Tarife und Dienste | Bestehende aufklappbare Karten erhalten. Planer-Standardmaße separat aufklappbar; vorhandene Maße sichtbar. Monats-/Jahreskopplung unverändert. |
| Benutzer | Such-/Sortiergruppe und kompakter Dialog. Passwortänderung bei bestehenden Benutzern optional aufklappbar, bei neuen sichtbar. |
| Rechnungs-Einstellungen | Nummernpräfix/-zähler, Stellen/Zahlungsziel und IBAN/BIC nebeneinander. Steuer- und Speicherverhalten unverändert. |
| Rechnung | Bestehende Dokument-/Aktionsstruktur und horizontal scrollbare Positionstabelle erhalten; einheitliche Aktionsabstände. |
| Backup | Bestehende Status-/Zeitplanbereiche und deutlich getrennte Wiederherstellung erhalten; keine Schwächung der Validierungs- oder RESTORE-Schranken. |
| Audit-Log | Suche primär; Aktion, Objekt und Zeitraum aufklappbar. Aktive Filterchips bleiben sichtbar, Entfernen öffnet und fokussiert das passende Feld. |
| Einstellungen | Kompaktere Konto-/Sitzungszeilen; Sicherheitsabschnitte und Fehler-/Wiederholungszustände erhalten. |
| Kalender | Monatsnavigation und Ansichtsschalter im gemeinsamen Kopf, mobil sauber untereinander. |
| Garagen / Hallen | Gleichwertige Standort-/Hallenkarten auf großen Bildschirmen zweispaltig, mobil einspaltig. |
| Planer | Werkzeugleistenabstände vereinheitlicht; Canvas, Platzierungsregeln, schmutziger Zustand und Speicherschutz unverändert. |
| Anmeldung | Bestehende kompakte Darstellung und aufklappbare Hilfe beibehalten; Authentifizierung erneut getestet. |
| Kundenportal | Anliegenformulare erst nach Auswahl sichtbar, Kontaktfelder paarweise, keine gestreckten Desktop-Grid-Zeilen. Deutsche und englische Abläufe erhalten. |

## Erhaltene Verträge und Randfälle

- Eingeklappte Felder bleiben Teil des Formulars; Schließen löscht oder deaktiviert keine Werte.
- Gruppen sind ausdrücklich pro Feld definiert, nicht aus fehlendem `required` abgeleitet.
- Validierungsfehler öffnen geschlossene Gruppen, bevor das fehlerhafte Feld fokussiert wird.
- Preisbindung, Null-/Leerwerte, Abrechnung je abgeschlossenem Zeitraum und die
  Zuständigkeit von Rechnung/Gefährt/Pauschale für Zahlungen sind unverändert.
- Ein fehlgeschlagener Gefährt-Lookup konnte beim Bearbeiten einer Zusatzkostenposition
  deren bestehende Zuordnung verlieren. Jetzt bleibt diese auswählbar und wird erhalten.
- Die Betragsvorschau ist nur eine Vorschau; gespeichert werden weiterhin die
  bisherigen API-Felder. Wiederkehrende Positionen ignorieren die einmalige Menge.
- Native Datumseingabe bleibt möglich; Heute/Gestern verwenden lokale Kalendertage.
- Kleine Inhaltsdialoge übernehmen keine breite Klasse eines vorherigen Formulars.

## Validierung

Die neuen synthetischen Prüfungen stehen in `tests/a11y/compact-workflows.spec.js`.
Sie prüfen konkrete Payloads, Preisbindung, versteckte Werte, Fehlerrückkehr,
Tarifkopplung, Pauschal-Gefährte, Portalsprachen, Tastatur und Dialog-Reflow samt
axe-Prüfungen bei 320, 390 und 1440 Pixeln. Bestehende Tests wurden nur an die
bewusst geänderten Einstiegspunkte angepasst (Portal/Audit öffnen, Bezeichnung
als Autocomplete-Combobox).

| Prüfung | Abschließendes Ergebnis |
| --- | --- |
| Komplette Playwright-Suite, 1 Worker, keine Wiederholungen | **99 bestanden**, 0 fehlgeschlagen, 0 übersprungen; 3,1 Minuten. |
| Neue Formularprüfungen, in obiger Gesamtzahl enthalten | **16 bestanden**. |
| Frontend-/Geometrie-Unit-Tests | **55 bestanden**, 0 fehlgeschlagen. |
| JavaScript-Syntax und Git-Whitespace-Prüfung | Bestanden. |
| CSP-Inline-Style-Guard einschließlich Selbsttests | Bestanden: 9 Umgehungsfälle erkannt, keine Fehlalarme, Anwendungscode sauber. |
| Impeccable Layout-Scan, vor und nach Umsetzung | Keine Funde (`[]`); nicht als Ersatz für die Sichtprüfung verwendet. |
| Docker-Build, Healthcheck, ausgelieferte JS-/CSS-Assets | Bestanden; SHA-256 der Test-Assets identisch mit den lokalen Quellen. |
| Vorschau | 44 referenzierte Bilder vorhanden; alle 19 Seiten sowie drei zentrale Dialoge in Desktop/Mobil. |

Der erste Durchlauf der neuen Tests zeigte fehlerhafte Test-Selektoren
(Pflichtfeldstern, geschlossene Benutzerkarte, unsichtbarer nativer Switch) und
eine während der Einblendanimation getrennt gemessene Feldposition. Diese wurden
im Testaufbau korrigiert, ohne die funktionalen Assertions abzuschwächen. Die
Sichtprüfung führte zu einem gemeinsamen Korrekturbatch: volle mobile Dialogbreite,
Betrag/Menge nebeneinander und kompakte Desktop-Zeilen im Portal. Anschließend
wurden die betroffenen Ansichten bestätigt; keine weitere visuelle Neuausrichtung.

Die Gliederung bleibt auf allen Größen gleich: notwendige Angaben zuerst,
ausdrücklich optionale Details danach, Speichern/Abbrechen im festen Dialogfuß.
Das Desktop-Feldraster wird auf schmalen Geräten einspaltig; lediglich das kurze
Betrag-/Menge-Paar bleibt zweispaltig. Fokus und DOM-Reihenfolge folgen diesem
Aufgabenpfad. Die vorhandenen Langtext-, Leerdaten-, niedrigen Dialog-,
Zwischenbreiten- und Tastaturprüfungen bestehen ebenfalls.

Ausführung aus `tests/a11y`, mit expliziter Wegwerf-Instanz:

```powershell
$env:PARKRR_BASE_URL='http://localhost:18099'
$env:PARKRR_E2E_ISOLATED='1'
node node_modules/@playwright/test/cli.js test --workers=1 --retries=0 --output=../../tmp/compact-full-suite
```

Die komplette Browser-Suite läuft ausschließlich auf einer Wegwerf-App mit
eigener temporärer PostgreSQL-Datenbank an `localhost:18099`. Der erste Versuch
wurde wegen eines fehlenden Host-Port-Mappings im internen Docker-Netz abgebrochen;
das separate Host-Netz betrifft nur die Test-App. Port 8099 und die Betriebsdatenbank
werden von den Tests nicht verwendet.

Die Go-/Datenbank-Geschäftslogik wurde in diesem Durchgang nicht geändert; die
frühere vollständige Go-/Race-Validierung ist in
[validation-20260912.md](validation-20260912.md) dokumentiert. Sie wird nicht als
neu ausgeführter Testlauf ausgegeben. Browser-/axe-Prüfungen ersetzen keine
vollständige Barrierefreiheitszertifizierung oder Tests auf physischen Geräten.

Generierte Ansichten liegen unter `.impeccable/review/compact-workflows/` und
`.impeccable/review/page-overhaul/` (synthetische Daten, Git-ignoriert).

Die aktuelle Galerie ist `.impeccable/review/compact-workflows/index.html`.
Die normale Docker-App auf Port 8099 wurde in diesem Durchgang nicht aktualisiert.
Änderungen werden auf `dev` committed, nicht gepusht. Testcontainer und Testnetze
werden nach dem Lauf entfernt; das lokale validierte Image bleibt verfügbar.

## Impeccable und Vorher-/Nachher-Vergleich

Die Überarbeitung in `bc5c86e` wurde mit Impeccable ausgeführt, insbesondere mit
den Vorgaben aus `distill`, `layout`, `operate` und `craft-floor`: vorhandene
Identität erhalten, Felder nach Aufgabe gruppieren, optionale Angaben ausdrücklich
definieren und den Fokus auch nach dem Aufklappen zuverlässig führen. Die
Browser-/Unit-Tests sind der zusätzliche Funktionsnachweis, kein Ersatz für den
Design-Skill. Deshalb wurde für den nachgeforderten Vergleich keine weitere
Änderungsrunde am Anwendungscode begonnen.

Der Vergleich ist in `.impeccable/review/before-after/index.html` ergänzt und in
der geöffneten Übersicht `.impeccable/review/compact-workflows/index.html`
direkt verlinkt. Zwei Vergleichsstände sind auswählbar:

- **Letzte Überarbeitung:** `539fb45` → `bc5c86e`.
- **Gesamte Überarbeitung:** `9756ae2` → `bc5c86e`.

Beide umfassen dieselben 19 Seiten und sieben Formularzustände: neue Person,
neues Gefährt, neue Zusatzkosten, neue Pauschale, neue Zahlung, Benutzer bearbeiten
und geöffneter Tarif. Pro Stand werden Desktop/hell (1440 × 900) und Mobil/dunkel
(390 × 900) aufgenommen. Beibehaltene Bereiche sind beim letzten Vergleich
ausdrücklich als solche beschrieben; ein unveränderter Bildschirm wird nicht als
neue Gestaltung ausgegeben.

Alle HTML-/CSS-/JS-/Bild-/Schriftdateien stammen aus dem jeweils bezeichneten
Git-Commit. API-Antworten sind synthetisch und identisch; Datum, deutsche Sprache,
Zeitzone und Pixeldichte sind festgelegt. Zwischen Aufnahmen wird das Dokument
neu geladen, damit keine Dialog-, Navigations- oder Anmeldezustände übertragen
werden. Es werden keine Formulare abgesendet und weder Docker noch Betriebsdaten
verwendet. Das Manifest enthält Commit-IDs sowie Asset-, Fixture- und Bild-Hashes.

Die reproduzierbare Erstellung und Galerieprüfung ist mit
`node tests/a11y/capture-before-after.cjs` möglich. Skript und HTML-Vorlage sind
versioniert; generierte Bilder und Galerie bleiben Git-ignoriert. Die
Funktionsresultate von 99 Browser- und 55 Unit-Tests oben gehören weiterhin zur
vorherigen Umsetzung; dieser Nachtrag verändert nur Vergleich und Dokumentation.

Abschließende Galerieprüfung: **156 Aufnahmen**, **104 Vergleichskombinationen**
erfolgreich geladen, Bildmaße und SHA-256 geprüft. Tastaturfokus,
Fehlermeldung bei fehlendem Bild, direktes Öffnen per Datei und Reflow bei
320/390/1440 Pixeln bestanden; keine schweren/kritischen axe-Funde und keine
ungefangenen JavaScript-Fehler. Desktop- und Mobilvergleich wurden zusätzlich
visuell kontrolliert. Mit `--verify-only` lässt sich dieser Lauf ohne erneute
Aufnahmen wiederholen; geänderte Revisionen, Fixtures oder PNGs werden abgelehnt.

Der ursprüngliche mobile Planer (`9756ae2`) hat bei 390 Pixeln Bildschirmbreite
eine 445 Pixel breite Vollseitenaufnahme. Diese historische Überbreite bleibt
im Vorher-Bild unverfälscht sichtbar; bei der aktuellen Version tritt sie nicht
auf. Die erste Prüfung setzte Bild- und Bildschirmbreite irrtümlich gleich;
jetzt werden die tatsächlichen PNG-Abmessungen erfasst und verglichen. Auch der
Browser-Kontext für axe wurde im Prüfskript korrigiert. Der abschließende Lauf
bestand mit unveränderten, über ihre Hashes geprüften Aufnahmen.
