# Dialogvorlage in Parkrr integriert – 13. September 2026

## Umfang

Die eigenständige HTML-Demo ist jetzt in ihren produktiven Teilen in Parkrr
integriert. Die vorherige [seitenweite Überarbeitung](compact-workflows-20260912.md)
bleibt bestehen; die 19 Ansichten werden nicht erneut durch Mockups ersetzt.
Impeccable (`distill`, `layout`, `harden`, `craft-floor`) wurde angewendet.
Die unabhängige Layoutsichtung und der davon getrennte mechanische Scan
bestätigten vier verbliebene Lücken gegenüber der Vorlage.

- Betrag, Menge und Ergebnis stehen als gemeinsame Gruppe vor dem Datum.
  Desktop: Eingaben und Summe nebeneinander. Mobil: Eingabepaar und anschließend
  Summe, ohne zusätzlichen Ergebniskasten; DOM- und Lesereihenfolge stimmen überein.
- Datum und Heute/Gestern verwenden eine gemeinsame, umbrechende Zeile.
  Das gilt auch für die vorhandenen Zahlungsformulare. Touch-Ziele bleiben groß.
- Enddatum und Gefährt-Zuordnung sind in den Kostenformularen unter „Weitere
  Angaben“ zusammengefasst. Vorhandene Werte öffnen die Gruppe beim Bearbeiten.
- Optionale Formulargruppen zeigen auch geschlossen die Zahl ausgefüllter
  Angaben. Ausgeblendete, nicht anwendbare Enddaten zählen nicht mit.

Erfassung einmaliger/monatlicher/jährlicher Zusatzkosten und Bearbeitung
wiederkehrender Kosten nutzen dieselbe Betragsvorschau. Die vorhandene
Katalog-/Freitexteingabe bleibt editierbar. Ein unzulässiges Enddatum öffnet die
eingeklappte Gruppe und fokussiert das Feld; die konkrete Fehlermeldung steht
auch inline. Unzuverlässig große Zahlen werden vor dem Absenden abgefangen.

## Bewusst erhalten

Speichern verwendet weiterhin die bestehenden API-Endpunkte und Payloads,
einschließlich echter Lade-/Fehlerzustände, Wiederholen und Schutz vor doppeltem
Absenden. Keine Demo-Daten, kein localStorage-Datenspeicher, kein CDN und kein
zusätzliches Framework wurden in die Anwendung übernommen. Abbrechen bleibt
Abbrechen; der Demo-Reset ersetzt keine produktive Aktion.

Zuordnungen, archivierte Gefährte beim Bearbeiten, verspätete Lookup-Antworten,
optionale Werte und Preisänderungen bleiben erhalten. Abrechnungsregeln,
Periodensperren, Zahlungszuordnung und Backend-Code sind unverändert. Leere oder
nullwertige einmalige Mengen behalten das bestehende API-Verhalten (Menge 1);
wiederkehrende Beträge werden nicht mit einer zuvor eingegebenen Menge multipliziert.

## Prüfungen

Die neuen Tests stehen in `tests/a11y/charge-integration.spec.js` und
`tests/frontend/charge-form-total.test.js`. Der erste Tastaturtest fokussierte den
Beschriftungs-Span statt des nativen Summary-Elements. Das Testziel wurde korrigiert;
anschließend bestanden alle sieben neuen Browserprüfungen. Keine funktionale
Assertion wurde abgeschwächt.

Die automatisierte Layoutprüfung meldete vor und nach der Umsetzung `[]`.
Die gemeinsame Sichtprüfung bei 1440/390/320 Pixeln bestätigte Betrag/Ergebnis-
Reihenfolge, Umbruch, sichtbare Aktionen und das erhaltene Erscheinungsbild.

| Abschließende Prüfung | Ergebnis |
| --- | --- |
| Vollständige Playwright-Suite, 1 Worker, keine Wiederholungen | **106 bestanden**, 0 fehlgeschlagen, 0 übersprungen; 3,4 Minuten. |
| Frontend-/Geometrie-Unit-Tests | **70 bestanden**, davon 15 neue Betragsfälle. |
| Neue Dialogprüfungen (in obiger Gesamtzahl enthalten) | **7 bestanden**. |
| JavaScript-Syntax, Git-Whitespace | Bestanden. |
| CSP-Inline-Style-Guard mit Selbsttests | Sauber; alle 9 Umgehungsfälle erkannt, keine Fehlalarme. |
| Docker-Build und ausgelieferte Test-Assets | Bestanden; JS-/CSS-Hashes identisch mit den Quellen. |

Die Browser-Suite lief auf einer eigenen Wegwerf-App an `localhost:18099` mit
frisch angelegter PostgreSQL-Datenbank, Testzugängen und aktivierten Passkey-Tests.
Die normale lokale App und deren Datenbank wurden nicht für schreibende Tests
verwendet. Kein neuer Go-/Race-Testlauf: Backend-Code und Geschäftslogik wurden
nicht verändert; frühere Ergebnisse werden nicht als neue Tests ausgegeben.

## Lokale Anwendung

Nach erfolgreicher Validierung wurde ausschließlich `parkrr-app` auf Port 8099
mit dem getesteten Image aktualisiert (`docker compose up -d --no-deps --no-build app`).
Healthcheck und ausgelieferte JS-/CSS-Hashes stimmen. Nicht angemeldete Zugriffe
auf `/api/auth/me` und `/api/persons` liefern weiterhin HTTP 401.
Die Identität von `parkrr-db` und die Backup-Volume-Zuordnung blieben unverändert.
Das vorherige App-Image ist als `parkrr:before-charge-integration-20260913` erhalten.
Die zwei eigens angelegten Testcontainer und ihr separates Netz wurden nach dem
Lauf entfernt. Dabei wurden ausschließlich deren synthetische, temporäre
Testdaten verworfen; die Betriebsdatenbank und andere Projekte blieben erhalten.

## Vorher / Nachher

Die vorhandene Galerie `.impeccable/review/before-after/index.html` ist auf den
integrierten Stand `cf4e9c0` aktualisiert und startet beim Zusatzkosten-Dialog.
Vergleichsstände bleiben `539fb45` (vor den Formularverfeinerungen) und `9756ae2`
(vor dem gesamten Seitenumbau). Alle **19 Seiten und sieben Formularansichten**
wurden mit denselben synthetischen Daten in Desktop/hell und Mobil/dunkel neu
aufgenommen: **156 Bilder**, **104 Vergleichskombinationen erfolgreich geprüft**.
Dateiöffnung, Bilddekodierung, Fehleranzeige, Tastatur und Reflow bei
320/390/1440 Pixeln bestanden; keine schweren/kritischen axe-Funde und keine
ungefangenen JavaScript-Fehler. Der Vergleich wurde visuell bestätigt.

Die geöffnete Übersicht `.impeccable/review/compact-workflows/index.html` verlinkt
direkt auf diesen Vergleich. Das Manifest enthält die tatsächlichen Commit-IDs,
Bildmaße und Hashes. Nur Generator, Vorlage und Dokumentation werden versioniert;
die reproduzierbaren Bilder bleiben Git-ignoriert. Änderungen liegen auf `dev`
und wurden nicht gepusht.
