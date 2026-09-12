# Umsetzung und Funktionsvalidierung – 11./12. September 2026

## Umfang

Die Änderungen aus [UI-Refinement](ui-refinement-20260910.md) und
[Page-by-page overhaul](page-by-page-overhaul-20260910.md) sind im Anwendungscode
enthalten: alle 17 internen Ansichten, Anmeldung und Kundenportal. Die
Vorher/Nachher-Galerie ist eine Dokumentation dieser Umsetzung, kein separates
Mockup. Fahrwerk-Identität, bestehende Bedienabläufe und Backend-Verträge bleiben
erhalten. Impeccable hat diesen Abschluss auf Fehlerzustände, Wiederholbarkeit,
Tastaturbedienung und Randfälle ausgerichtet; es wurde keine weitere visuelle
Neuausrichtung begonnen.

## Gefundene und behobene Probleme

| Problem | Korrektur | Nachweis |
| --- | --- | --- |
| Fakturierte, eigenständige Zusatzkosten konnten trotz bezahlter Rechnung als offen erscheinen und direkte Zahlungskontrollen anbieten. | Für alle fakturierten Zusatzkosten ist der Rechnungsstatus maßgeblich. Filter, Kennzeichnung und schreibgeschützte Zahlungsanzeige verwenden dieselbe Regel. | 12 neue tabellarische Unit-Tests; Browserprüfung des Filters und der gesperrten Direktzahlung. |
| Ein HTTP 401 beim Laden der Kontositzungen blieb als lokaler Einstellungsfehler stehen. | Der Fehler erreicht wieder die zentrale Behandlung abgelaufener Anmeldungen. | Browserprüfung: Einstellungen verlassen, Anmeldung sichtbar. |
| Ein erfolgreich übertragener, aber ungültiger QR-Bildinhalt konnte dauerhaft als geladen gelten. | Erst erfolgreiche Bilddekodierung setzt den Ladezustand; Fehler erlauben einen neuen Versuch, Objekt-URLs werden freigegeben. | Browserprüfung mit HTTP 200/ungültigem Bild, anschließend erfolgreichem SVG und ohne unnötige weitere Anforderung. |
| Der Audit-Indextest war von zufällig bereits vorhandenen Datensätzen abhängig: zunächst übersprungen, später mit Seq Scan fehlgeschlagen. | Eigene 20.000 historische Datensätze, normale GIN-Wartung und ANALYZE in einer zurückgerollten Transaktion. Keine erzwungenen Planeroptionen oder abgeschwächten Index-Assertions. | Drei abschließende gezielte Durchläufe mit beiden Trigramm-Indizes; jeweils 20.000 eingefügte und nach Rollback 0 verbleibende Datensätze. |
| Eine Migrationsprüfung schloss die administrative DB-Verbindung vor dem Aufräumen ihrer temporären Datenbank. | Verbindungsende ebenfalls über `t.Cleanup`, sodass die Aufräumreihenfolge stimmt. | Gezielter Migrationstest erfolgreich; erneute Prüfung im finalen Backend-Lauf. |

Zusätzlich wurden kalenderabhängige synthetische Prüfungen auf den 10.09.2026
fixiert. Ein anfänglicher Fehler im neuen durchgängigen Browsertest war ein
unpassender Locator für ein Pflichtfeld; er wurde korrigiert, nicht als
Produktfehler behandelt. Der anschließende vollständige Browserlauf benötigt
keine Wiederholungsversuche.

## Testergebnisse

| Prüfung | Ergebnis |
| --- | --- |
| Frontend-Unit-Tests: Authentifizierungszustand, Zahlungsstatus und Planergeometrie | **55 bestanden**, 0 übersprungen. |
| Gesamte Playwright-Suite, Chromium, ein Worker, keine Retries | **79 bestanden**, 0 fehlgeschlagen, 0 übersprungen, 0 flaky; 172,9 Sekunden. |
| Erster kompletter Linux-Go-Lauf mit Race Detector und realem PostgreSQL | **418 Top-Level-Tests / 513 inklusive Untertests bestanden**; 2 umgebungsabhängige Prüfungen übersprungen und anschließend gesondert behandelt. |
| Finaler serieller Linux-Go-Lauf mit frischer DB und normalisierten Zeilenenden | **419 Top-Level-Tests / 514 inklusive Untertests bestanden**, 9 Pakete erfolgreich, keine Fehler oder gemeldeten Datenrennen. Nur der separat geprüfte große Restore bleibt in diesem Lauf übersprungen. |
| Verschlüsseltes Backup und tatsächliches Restore unter Ressourcenlimits | **Bestanden**: 34 × 1 MiB Nutzdaten, 256 MiB Containerlimit, 32 MiB temporärer Speicher; einschließlich falschem Schlüssel und abgelehntem Restore mit Rollback. |
| `go mod verify`, `go vet ./...` | Erster Gesamtlauf und finaler Vorlauf bestanden. |
| JavaScript-Syntax, CSP-Inline-Style-Guard einschließlich dessen Selbsttests | **Bestanden**. |
| Git-Whitespace-Prüfung mit `core.whitespace=cr-at-eol` | **Bestanden**; berücksichtigt die vorhandenen Windows-Zeilenenden. |
| Docker-Build und Healthcheck der isolierten App | **Bestanden**. |
| Ausgeliefertes JavaScript, CSS und Geometriecode gegen lokale Quellen | **SHA-256 identisch**. |
| Lokale App nach Aktualisierung auf Port 8099 | **Bestanden**: Healthcheck, Image-Identität, Asset-Hashes und Cache-Fingerprints, HTTP 401 für nicht angemeldete Zugriffe auf `/api/auth/me` und `/api/persons`, zusätzlicher Login-/axe-Browsertest. |

Die Zahlen für Top-Level-Tests und Untertests sind alternative Zählweisen,
keine addierbaren Testmengen. Der allgemeine Go-Lauf überspringt den großen
Restore absichtlich ohne dessen besondere Umgebung; der separate Restore-Lauf
führt ihn tatsächlich aus.

**Offene Messgrenze:** Die Coverage-Werkzeuge melden im finalen Lauf 75,3 %, zählen
`server.go` aber weiterhin mit auffälligen 6.384 Statements. Dieses bereits in der
CI-Dokumentation beschriebene Zählproblem bleibt auch unter Linux mit
normalisierten LF-Zeilenenden bestehen; die frühere Erklärung allein durch
Windows/CRLF ist damit nicht bestätigt. Der Prozentwert wird deshalb ausdrücklich
nicht als belastbarer Qualitätsnachweis verwendet. Die Ursache im
Coverage-Reporting bleibt offen, die Testausgänge sind davon unabhängig. Für
diese Gegenprüfung wurde nur eine Wegwerfkopie normalisiert, nicht die
Arbeitskopie verändert.

## Was die Browserprüfungen tatsächlich abdecken

- Alle Ansichten mit synthetischen, gefüllten Datensätzen in Desktop/Mobil und
  Hell/Dunkel; Zwischenbreite 768 px, lange Inhalte, schmale und niedrige Dialoge,
  Tastaturfokus, Skip-Link, Zwangsfarben und ausgewählte axe-A/AA-Prüfungen.
- Echte Anmeldung mit falschem und richtigem Passwort, TOTP und Recovery-Code,
  Registrierung/Anmeldung mit virtuellem WebAuthn-Authentikator.
- Echte Person anlegen und bearbeiten, Detailabschnitte per Link öffnen,
  Zusatzkosten anlegen, bezahlt/offen setzen und Daten nach Neuladen prüfen.
- Sammelabholung, Kalender, tatsächlicher PDF-Anhang und Foto-Upload,
  CSV-Exporte, serverseitige Suche und ihre Tastatur-/Antwortreihenfolge.
- Planeroberfläche, Messwerkzeug, DXF-Import und -Unterlage, kollisionsfreie
  automatische Anordnung, Vorlagen-Roundtrip und Belegungsdaten.
- Portal-Sprache mit Speicherung sowie echte Anfrage einreichen und übernehmen.
- Mit synthetischen Fehlerantworten: temporäre Ladefehler, erneuter Versuch,
  erhaltene Formulareingaben, exakt geprüfte Rechnungs-/Kontakt-Payloads,
  Doppelklickschutz, Leser-/Bearbeiterrechte und verspätete Antworten nach
  einem Routen- oder Portalwechsel.

Die vollständige Go-Suite ergänzt dies um Datenbankmigrationen, serverseitige
Validierung und Rechte, Rechnungs-/Zahlungsregeln, Periodensperren, Portal,
Dateien/PDFs, Backups, Authentifizierung sowie absichtlich konkurrierende
Serverabläufe. Reine Unit-Tests, DB-Integrationstests und Browserprüfungen werden
hier bewusst nicht als austauschbare Nachweise bezeichnet.

## Wiederholbarkeit und Schutz bestehender Daten

Alle schreibenden Prüfungen laufen in einem eigenen Docker-Netzwerk mit eigenem
PostgreSQL-Container. Go-Erstlauf, Go-Abschlusslauf, Browser-App und großer Restore
verwenden getrennte Datenbanken. Die normale Datenbank `parkrr-db` ist kein
Testziel. Port 18099 gehört der Wegwerf-App, Port 8099 der normalen lokalen App;
Port 8080 und Treckrr bleiben unangetastet.

Die CI-Konfiguration enthält jetzt alle Frontend-Unit-Tests und den neuen
isolierten Datensatz-/Zahlungsablauf. WebAuthn ist in der Wegwerf-CI-App aktiviert,
externe Passwortleck-Abfragen nur dort deaktiviert. Go-Pakete laufen mit `-p 1`,
damit Migrationen/Restore nicht gleichzeitig das gemeinsam genutzte Testschema
verändern; der Race Detector bleibt aktiv. Ein entfernter CI-Lauf wurde nicht
ausgelöst und wird nicht als bestanden behauptet.

Generierte Vergleichsbilder, Testausgaben und verschachtelte `node_modules`
werden nicht mehr in den Docker-Buildkontext aufgenommen. Dies ist eine
Build-Verbesserung, kein gemessener Nachweis schnellerer Endnutzer-Ladezeiten.

Relevante Befehle, ausschließlich gegen vorbereitete Wegwerf-Datenbanken:

```sh
node --check web/static/js/app.js
node --test tests/geometry/geometry.test.js tests/frontend/*.test.js
go mod verify
go vet ./...
go test -race -p 1 -count=1 -json -covermode=atomic -coverprofile=coverage.out ./...
```

Aus `tests/a11y`, mit eigener Wegwerf-App und passenden Testzugangsdaten:

```sh
PARKRR_BASE_URL=http://localhost:18099 PARKRR_E2E_ISOLATED=1 \
  node node_modules/@playwright/test/cli.js test --workers=1 --retries=0
```

Weitere Hinweise stehen in [tests/a11y/README.md](../tests/a11y/README.md).
Lokale Rohdaten bleiben unter `tmp/validation-20260911/` (Git-ignoriert), darunter
`browser-final.json`, `go-test.json`, `go-final.json`, `deployed-login.json`,
`restore-check.txt` und die Coverage-Dateien.

Nach Abschluss wurden die sechs eindeutig mit dieser Prüfung gekennzeichneten
Testcontainer, deren anonyme Testdaten-Volumes und das eigene Testnetz entfernt.
Die synthetischen Daten wurden dabei verworfen; sie werden nicht aufbewahrt oder
wiederhergestellt. Die normale App und ihre Daten-Volumes bleiben bestehen,
ebenso die lokalen Prüfprotokolle und die Vorher/Nachher-Galerie.

## Lokale Bereitstellung

Am 12.09.2026 wurde ausschließlich `parkrr-app` mit dem bereits getesteten Image
aktualisiert: **http://localhost:8099**. Die App ist gesund. Container-ID und
Startzeit von `parkrr-db` sind gegenüber dem Zustand vor der Aktualisierung
identisch; der Datenbankcontainer wurde weder neu erstellt noch neu gestartet.
Keine schreibenden Funktionstests wurden gegen die normale Instanz ausgeführt.

Ausgelieferte Asset-Fingerprints: `app.js?v=6f6dcf7622` und
`style.css?v=9cbbb0d04b`; zusätzlich wurden die vollständigen SHA-256-Hashes von
JavaScript, CSS und Geometriecode mit den Arbeitsdateien verglichen.

Das vorherige Image bleibt lokal als `parkrr:before-ui-validation-20260912`
erhalten. Ein gezielter App-Rückwechsel wäre möglich mit:

```sh
docker tag parkrr:before-ui-validation-20260912 parkrr:latest
docker compose up -d --no-deps --no-build --pull never app
```

Dies ist ein Image-Rückwechsel, kein Zurücksetzen oder Wiederherstellen von
Geschäftsdaten. Validierung und lokale Bereitstellung erfolgten vor dem Git-Commit.
Das abschließende Änderungspaket wird gemäß Projektvorgabe lokal auf `dev`
committet; den Sync/Push übernimmt der Nutzer. Kein öffentlicher Rollout oder
Push wurde ausgeführt.

## Grenzen der Aussage

Grüne Tests sind keine Garantie für jede denkbare Kombination. Nicht live
geprüft wurden externe SMTP-Zustellung, echte S3-Dienste, physische Passkeys,
reale Mobilgeräte, Safari/Firefox, reale Drucker oder jeder Drag-/Dreh-/Zoompfad
im Planer. Ausgewählte axe-Prüfungen sind keine vollständige
Barrierefreiheitszertifizierung. Es wurde kein neuer externer Security-Scan oder
repräsentativer Last-/Core-Web-Vitals-Test durchgeführt.
