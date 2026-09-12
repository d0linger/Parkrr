# Seitenweite UI-Verfeinerung – 12. September 2026

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
