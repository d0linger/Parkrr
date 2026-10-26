# Parkrr — Betreiber-Handbuch

Für die Person, die Parkrr BETREIBT: installiert, sichert, aktualisiert und im
Ernstfall wiederherstellt. Die Bedienung der Anwendung selbst erklärt sich in der
Oberfläche; dieses Handbuch behandelt alles außenherum.

Die README bleibt die Referenz für jede einzelne Umgebungsvariable — hier steht,
**wann man was tut und warum**.

---

## 1. Die täglichen fünf Minuten

Es gibt genau drei Stellen, die ein Betreiber regelmäßig ansieht:

1. **Dashboard → Backup-Kachel.** Grün = der letzte geplante Lauf war
   erfolgreich UND pünktlich (gemessen am eigenen Cron, nicht an einer fixen
   Frist). Rot heißt: der jüngste brauchbare Wiederherstellungspunkt altert.
2. **Dashboard → Kundenwünsche** (erscheint nur, wenn offen): Stammdaten-Wünsche
   übernehmen oder ablehnen, Abholtermine bestätigen.
3. **Audit-Log** (Admins): das Änderungsprotokoll. Der Knopf „E-Mail-Versand"
   daneben beantwortet „ist die Mahnung rausgegangen?", der „Revisionsexport"
   liefert das Protokoll als prüfbare Datei (SHA-256-Hashkette, siehe §6).

Wer `PARKRR_ALERT_EMAIL` gesetzt hat, bekommt Punkt 1 als Mail, wenn ein
geplantes Backup fehlschlägt — dann reicht der Blick in den Posteingang.

### Dashboard: Jahresauswahl und Umsatzprognose

Die Jahresauswahl enthält Jahre mit Mietzeiträumen, Pauschalen, Zusatzkosten oder
Zahlungen sowie immer das aktuelle Serverjahr. Die Pfeile überspringen echte
Datenlücken; die Jahreszahl ist auch direkt auswählbar. Eine kostenlose Buchung
ist ein Vorgang und verschwindet nicht wegen 0 € Umsatz. Mehrjährige Verträge
berücksichtigen alle betroffenen Jahre, auch bei archivierten Vorgängen. Das
Installationsdatum ist keine Grenze für nachgetragene historische Daten.

Im Umsatzdiagramm lässt sich zwischen **Monat** und **Jahr** wechseln.
**Prognose anzeigen** ist zunächst ausgeschaltet. Erst eingeschaltet ergänzt sie
die bisher aufgelaufenen Mieterlöse um erwartete Restbeträge aus bestehenden
Vereinbarungen. Die Monatswerte und die Jahresdarstellung verwenden dieselben
Beträge. Das nächste Jahr ist nur innerhalb dieser Planung auswählbar; die
übrigen Dashboard-Kennzahlen bleiben beim ausgewählten tatsächlichen Datenjahr.

Cent-Rundungen werden je Vertrags- oder Kostenquelle über die Monatsgrenzen
fortgeschrieben. Dadurch bleibt die Jahressumme erhalten, ohne Rundungsreste
auf andere Verträge oder Monate zu verteilen. Tatsächliche Gutschriften behalten
ihren negativen Betrag; die Vorschau erzeugt keine künstlichen Gutschriften.

Unter **Annahmen & Details** sind Reservierungen und der Vorjahresvergleich
separat zuschaltbar. Reservierungen sind geplante Mieterlöse, keine garantierten
Zahlungen. Die Vorschau verwendet Mietbeginn und Mietende; widersprüchliche
Reservierungsdaten werden als Prüfhinweis ausgewiesen. Verträge ohne Enddatum
werden nur für den gewählten Prognosezeitraum unverändert fortgeführt. Befristete
Verträge werden nicht automatisch verlängert. Bei Pauschalen wird die
zugeordnete Fahrzeugmiete nicht zusätzlich gezählt, auch nicht automatisch nach
Ablauf der Pauschale. Erfasste laufende Zusatzkosten und bereits datierte
Einmalposten fließen ein; das reine Strombedarf-Häkchen erzeugt keinen Preis.

Der Vorjahresvergleich ist eine getrennte Referenz, keine zusätzlich erwartete
Buchung. Er fehlt ausdrücklich, wenn kein Datenjahr vorhanden oder das
Vergleichsjahr noch nicht abgeschlossen ist. Historische Werte beruhen auf den
vorhandenen Vertragsdaten, nicht auf vollständig eingefrorenen damaligen
Vertragsständen. Testjahre eignen sich nicht als Nachweis einer Saisonalität.
Die bisherige einfache Jahreshochrechnung heißt **Lineare Hochrechnung**.

**Keine Geldbewegung:** Die Prognose liest Daten, speichert keine Beträge und
ändert weder Preise, Zahlungen, offene Forderungen, Rechnungen noch Steuerwerte.
Sie prognostiziert Mieterlöse, nicht den Zeitpunkt tatsächlicher Zahlungseingänge.
Ein Rechnungsablauf ist dafür nicht erforderlich. Die Erweiterung benötigt keine
neue Datenbankmigration. Ein späteres Update muss trotzdem auf sonstige bereits
ausstehende Migrationen geprüft werden.

## 2. Backups: einrichten, prüfen, wiederherstellen

**Einrichten:** `PARKRR_BACKUP_KEY` setzen (mindestens 32 Zeichen, getrennt vom
Session-Secret aufbewahren!) und ein Ziel: `PARKRR_BACKUP_DIR` (gemountetes
Volume) und/oder `PARKRR_S3_*`. Zeitplan und Aufbewahrung stellt man danach in
der Oberfläche ein (Backup-Reiter): Cron je Ziel, „Behalten (Anzahl)" und seit
neuestem „Mindestens aufbewahren (Tage)" — Letzteres ist ein Boden, der
verhindert, dass sieben Läufe an einem Nachmittag die Historie auf Stunden
eindampfen.

**Der Schlüssel ist das Backup.** Ohne `PARKRR_BACKUP_KEY` ist jedes Archiv
wertlos verschlüsselter Datenmüll. Den Schlüssel NICHT nur auf dem Server
ablegen, der gesichert wird.

**Prüfen:** jedes Volume-Backup wird nach dem Schreiben automatisch rückgelesen
und probeentpackt (pg_restore --list); nur geprüfte Archive verdrängen alte.
Trotzdem gilt: einmal im Quartal eine echte Wiederherstellung in eine
Wegwerf-Datenbank fahren — ein Backup, das nie restauriert wurde, ist eine
Hoffnung, kein Backup.

**Wiederherstellen im Browser (optional):** Mit
`PARKRR_BROWSER_RESTORE=true` erscheint im Backup-Reiter der Restore-Bereich.
Die Sicherung zuerst validieren und anschließend die angezeigte, an ihre
SHA-256-Prüfsumme gebundene Bestätigung eingeben. Die Anmeldung des Admins darf
dabei höchstens zehn Minuten alt sein. Parkrr schließt neue Anfragen, wartet auf
laufende Anfragen und Hintergrundjobs, hält alle Replikate über eine gemeinsame
Datenbank-Lease an und stellt atomar wieder her. Danach laufen Migrationen und
Konsistenzprüfungen; sämtliche Sitzungen werden widerrufen.

Währenddessen bleibt `/healthz` erreichbar, `/readyz` liefert 503 und der Browser
zeigt den dauerhaften Restore-Status. Bei einem Prozessabbruch wiederholt keine
andere Instanz blind den destruktiven Schritt: sie übernimmt nur die idempotente
Reparatur und Prüfung. Ein abgebrochener Vorgang endet deshalb sichtbar als
fehlgeschlagen und verlangt eine Kontrolle des Datenstands.

Der Browser nimmt Archive bis 1 GiB an. Der Archivschlüssel wird nur im Speicher
der koordinierenden Instanz gehalten und nie in der Datenbank gespeichert. Die
Funktion ist standardmäßig aus; beim bloßen Update wird sie nicht aktiviert.
Bei mehreren App-Replikaten muss die Variable auf allen Replikaten denselben Wert
haben; eine nicht aktivierte Replik hält den exklusiven Restore sonst sicher an.

**CLI-Fallback / bewusst offline:**

```bash
# Zuerst ALLE Parkrr-App-Instanzen stoppen. Der Befehl verweigert den Restore,
# solange auch nur eine Instanz ihre Datenbank-Lease hält.

# Aus einer Archivdatei (destruktiv — überschreibt die Datenbank, atomar):
parkrr restore /backups/parkrr-2026-09-07-030000.dump.enc

# Aus S3: Archiv im Backup-Reiter herunterladen, dann ebenfalls offline mit
# `parkrr restore <datei>` einspielen.
```

Danach Parkrr wieder starten. Fehlende Migrationen werden vom Restore-Befehl
eingespielt; alle Sitzungen werden verworfen, daher melden sich alle Benutzer neu
an. Neue Sicherungen verwenden ein stückweise authentifiziertes Format und bleiben
dadurch auch bei großen Datenbanken speicherbegrenzt; ältere Sicherungen bleiben
lesbar.

## 3. Updates einspielen

Der Weg ist: `dev` → Pull Request → `main` → Deploy (main ist Produktion).

1. Vor dem Update auf die Backup-Kachel sehen: Grün? Wenn nicht: erst Backup.
2. Neues Image ziehen, Container neu erstellen. Migrationen laufen beim Start
   automatisch und sind per Checksumme gegen nachträgliche Veränderung gesichert
   — eine veränderte, bereits angewandte Migration stoppt den Start absichtlich.
3. Die Anwendung meldet neuen Code den offenen Browsern selbst („Neue Version
   verfügbar → Neu laden").

Rollback: voriges Image-Tag deployen. Migrationen sind vorwärtsgerichtet — wenn
eine neue Version Migrationen mitbrachte, ist der saubere Rückweg die
Wiederherstellung des Backups von vor dem Update (deshalb Schritt 1).

## 4. Die Schalter, die Verhalten ändern (alle Default AUS)

| Schalter | Wirkung, wenn eingeschaltet |
|---|---|
| `PARKRR_REQUIRE_2FA` | Konten ohne TOTP/Passkey erreichen nur noch die 2FA-/Passkey-Einrichtung. Vorher prüfen: haben alle Admins einen zweiten Faktor? |
| `PARKRR_PASSKEY_ONLY` | Passwort-Login abgeschaltet. Verlangt WebAuthn (`PARKRR_WEBAUTHN_RP_ID`) — und mindestens ein Konto MIT registriertem Passkey, sonst sperrt man sich aus. Reihenfolge: Passkeys einrichten → testen → Schalter setzen. |
| `PARKRR_AUTO_INVOICE_CRON` | Automatischer Rechnungslauf (z. B. `0 6 1 * *`). Nutzt denselben Pfad wie der „+ Rechnung"-Knopf; abgerechnete Perioden werden nie doppelt fakturiert. Vorher die Verkäufer-Pflichtangaben (Abrechnung → Einstellungen) vollständig ausfüllen, sonst bricht der Lauf mit klarer Meldung ab. |
| `PARKRR_TIMEZONE` | Geschäftszeitzone (z. B. `Europe/Vienna`). Wichtig, wenn der Container in UTC läuft: zwischen 00:00 und 02:00 Wiener Zeit ist in UTC noch gestern — Zahlungen bekämen das Datum von gestern. |
| `PARKRR_ALERT_EMAIL` | Betriebsalarme (fehlgeschlagene Backups) per Mail. Braucht konfiguriertes SMTP. |

Dazu kommen Schalter in der Oberfläche, die nach außen wirken und ebenfalls
ausgeschaltet ausgeliefert werden: die Mail-Automatik und die Preis-Mitteilung
der Wertsicherung (§10). Welche Reihenfolge sich beim Einschalten bewährt, steht
in §11.

### Steuerbereich: zuerst nur lesen, später aktivieren

Der Steuerbereich ist standardmäßig im **Lesemodus**. Administratoren können
Jahre wechseln, vorhandene Einnahmen, Ausgaben, Anlagen und Belege prüfen,
die unverbindliche Jahreswechsel-Simulation ansehen und CSV, PDF oder ZIP
herunterladen. Der normale Ablauf „Buchung → bezahlt markieren → abholen“
setzt weder den Steuerbereich noch einen Rechnungsprozess voraus.

Schreibaktionen sind auch serverseitig gesperrt: Ausgaben und Vorlagen anlegen,
Ausgaben stornieren, Belege hochladen, Anlagen erfassen, Steuerobjekte und
Einstellungen ändern, Zahlungen steuerlich zuordnen sowie Jahre sperren oder
entsperren. Der Lesemodus ist kein leerer Demodatenbestand: bereits vorhandene
Ausgaben, AfA und Zuordnungen gehen weiterhin in die Vorschau ein.

**Später aktivieren:** `PARKRR_TAX_WRITES_ENABLED=true` in der
App-Konfiguration setzen und die Anwendung neu starten. Bei Docker den
App-Container mit der geänderten Konfiguration neu erstellen; ein bloßes
`docker restart` übernimmt keine geänderten Umgebungsvariablen. Bei mehreren
App-Replikaten denselben Wert auf allen Replikaten setzen. Anschließend ist die
Bearbeitung für Administratoren freigegeben. Es werden dadurch keine Rechnungen,
Ausgaben oder Importe erzeugt und keine vereinbarten Preise neu berechnet.
Eine Freigabe in dieser Dokumentation aktiviert keine laufende Instanz.

**Wieder nur lesen:** auf `false` zurückstellen und genauso neu starten bzw.
neu erstellen. Daten werden dabei weder gelöscht noch zurückgesetzt.
Bereits abgeschlossene Steuerjahre bleiben gesperrt und schützen weiterhin
auch normale Zahlungskorrekturen. Zum Entsperren ist die bewusste Aktivierung
der Steuer-Schreibfunktionen nötig; der Lesemodus umgeht diesen Schutz nicht.

Die Aktivierung ist keine steuerliche Abnahme. E1b bleibt eine Vorschau,
KU-Voraussetzungen und maßgebliche Jahres-/Vorjahresumsätze müssen fachlich
geprüft werden. Eine Importvorschau für externe AfA-/Jahreskorrekturen ist vorhanden;
die dauerhafte Übernahme und ein vollständiges jahresbezogenes E1b-Mapping fehlen.
Eine vorhandene einfache AfA-Berechnung ersetzt diese Prüfung nicht.

### Optionale Jahresprüfung ohne Speicherung

Unter „Jahresprüfung vorbereiten · Vorschau“ lassen sich für 2025 und 2026
gesondert ermittelte Jahres-/Vorjahresumsätze eingeben. Leere Angaben bleiben
unbekannt; Zahlungseingänge werden nur zum Vergleich angezeigt, nicht als
maßgeblicher Umsatz übernommen. Die Vorschau ist keine Bestätigung der
KU-Berechtigung. Quelle und Prüferangabe sind deine Eingaben, keine von Parkrr
verifizierten Nachweise.

„Einzelnen Jahreswechsel prüfen“ benötigt tatsächliche Zahlung, vertragliche
Fälligkeit, wirtschaftliches Bezugsjahr und die Angabe, ob der Vorgang regelmäßig
wiederkehrt. Einnahmen und Ausgaben werden unterstützt. Das hinterlegte
15-Tage-Prüffenster stützt sich ergänzend auf eine archivierte BMF-Auslegung;
es ersetzt keine aktuelle Einzelfallprüfung. Kein Ergebnis verschiebt Beträge
in den Berichten. Der JSON-Download ist ein lokaler Prüfentwurf, kein Importformat
für bestätigte Jahreskorrekturen. Beim Verlassen der Ansicht gehen Eingaben
verloren, sofern du keinen Entwurf heruntergeladen hast.

### Externe Jahreswerte als Datei prüfen

„Externe Jahreswerte prüfen · Importvorschau“ ist eine getrennte, standardmäßig
eingeklappte Ansicht für Administratoren. Sie funktioniert auch im Lesemodus und
bei abgeschlossenen Jahren, weil sie keine Steuerdaten liest oder schreibt.
Wähle ein vorhandenes Steuerobjekt und eine UTF-8-JSON-Datei (maximal 64 KiB,
1 bis 100 Einträge). Bei genau einem Objekt ist es vorausgewählt. Erst „Datei
prüfen“ sendet den Inhalt an den eigenen Parkrr-Server, nicht an einen externen
Dienst. Jahr und Objekt der geprüften Datei müssen zur Ansicht passen.

Die Beispieldatei enthält erfundene Werte und Herkunftsangaben; vor einer echten
Prüfung vollständig ersetzen. Dateiformat Version 1: `year` (2025 oder 2026),
`property_id`, `source`, `reviewer` und `entries`. Jeder Eintrag enthält eine
eindeutige `external_id` (1–64 Zeichen: Buchstaben/Ziffern, danach auch `._:-`),
`kind`, `amount_eur` als Dezimaltext und `description`. Herkunft, Prüferangabe
und Beschreibung sind einzeilig, nicht leer und jeweils höchstens 1000 UTF-8-Bytes.

- `income_adjustment`: positive oder negative Einnahmenkorrektur.
- `expense_adjustment`: positive oder negative Werbungskostenkorrektur.
- `external_depreciation`: nicht negativer externer AfA-Wert, **kein Zusatz** zur
  bereits in Parkrr erfassten AfA.

Beträge benötigen einen Dezimalpunkt und höchstens zwei Nachkommastellen,
keine Tausendertrennung oder Exponentialnotation; zum Beispiel `"-20.00"`.
Einzelbeträge und endgültige Gruppensummen sind auf jeweils eine Milliarde EUR
im Betrag begrenzt. Doppelte JSON-Felder, unbekannte Felder, doppelte externe IDs
innerhalb der Datei und ungenaue Beträge werden abgewiesen. Die Vorschau zeigt
drei getrennte Summen, keinen neuen Jahresüberschuss. Bestehende Zahlungen,
Werbungskosten, AfA, E1b und ZIP-Exporte bleiben unverändert.

Quelle und Prüferangabe sind nicht verifiziert. Die API prüft keine Existenz der
Objekt-ID; die Oberfläche gleicht sie mit dem gewählten vorhandenen Objekt ab.
Es gibt keinen Abgleich gegen Bestandsdaten oder früher geprüfte Dateien.
Wiederholtes Prüfen erzeugt keinen Eintrag. „Vorschau herunterladen“ sichert einen
Prüfnachweis im Format `parkrr-tax-import-preview`, kein FinanzOnline- oder
Übernahmeformat. Zum erneuten Prüfen die ursprüngliche Eingabedatei verwenden.
Eine neue Datei, eine andere Objektwahl oder „Auswahl verwerfen“ entfernt das
bisherige Ergebnis; nach einem Fehler bleibt die Auswahl für einen Retry erhalten.
Beim Verlassen der Ansicht wird nichts lokal gespeichert.

### Schutz bestehender Preise und Periodenzahlungen

Neue normale Buchungen übernehmen den aktuellen Tarif. Bestehende Buchungen
behalten ihren vereinbarten Betrag, auch bei künftigem Mietbeginn. Bezieht eine
Altbuchung ihren Preis noch dynamisch aus dem Tarif, wird dessen betroffener
Preis nicht geändert (409 mit Anzahl der abhängigen Buchungen). Bestätige solche
Preise nach Prüfung einzeln oder lege einen neuen Tarif an. Es gibt keinen
automatischen Preis-Backfill. „Erneut einstellen“ kopiert weiterhin bewusst den
alten vereinbarten Preis, einschließlich Sonder- und Nullpreisen.

Vorläufiger Historien-Schutz: Betrag ersetzen, Bezahlt zurücksetzen oder die
Pauschale/Nebenkostenquelle löschen wird mit 409 abgewiesen, sobald dadurch
vorhandene Periodenzahlungen verloren gingen. Das gilt auch für stornierte
Historie. Betrag, Zahlungsdatum, Zuordnungen und Quelle bleiben unverändert.
Neue einzelne Perioden können weiter bezahlt werden; eine identische aktive
Periodenmarkierung erzeugt keinen zweiten Eingang.

Der Pauschalen-Gesamtschalter setzt intern alle Perioden neu auf und ist deshalb
bei vorhandenen Zahlungen ebenfalls gesperrt. Der Nebenkosten-Gesamtschalter darf
neue Perioden ergänzen, aber keine vorhandenen Teilbeträge ersetzen. Für echte
Korrekturen fehlt noch der geprüfte Perioden-Storno-/Neubuchungsablauf. Nicht als
Umgehung den Beleg löschen oder eine zusätzliche Zahlung erfassen. Ein bloßes
Zurücksetzen des Bezahlt-Häkchens wäre ohnehin kein tatsächlicher Geldrückfluss.

### Steuer-ZIP und Ausrollprüfung

Das ZIP liest alle Daten einschließlich Belegen aus demselben lesenden
Datenbank-Snapshot. Nach dessen Materialisierung wird die Verbindung freigegeben;
ein langsamer Download hält sie nicht offen. Fehlende Altverknüpfungen werden
weiterhin als Lücken ausgewiesen, nicht rekonstruiert.

Für Belege gilt standardmäßig ein Gesamtlimit von 24 MiB unkomprimierter Daten
je Export: `PARKRR_TAX_EXPORT_MAX_RECEIPT_MB` (ganze Zahl von 1–1024). Ein leerer
Wert nutzt den Standard; ein ungültiger Wert verhindert den Anwendungsstart,
statt unbemerkt ein anderes Limit zu verwenden. Die Belege liegen während des
Downloads in einer privaten temporären Datei. Im gehärteten Container sind
32 MiB `/tmp` vorgesehen. Vor größeren Limits ein ausreichend großes privates
Volume über `TMPDIR` bereitstellen; parallele Exporte benötigen entsprechend
mehr Platz. Zu wenig Speicher beendet den Export ohne Finanzänderung.

Diese Ergänzungen enthalten keine SQL-Migration und keine automatische
Bestandskorrektur. Vor dem Ausrollen trotzdem Zielversion und bereits
ausstehende ältere Migrationen prüfen, Finanzsummen/Legacy-Preise abgleichen,
ein verschlüsseltes Backup erstellen und genau dieses isoliert wiederherstellen.
Die synthetischen Restore-Tests ersetzen keine Prüfung deines tatsächlichen
Backups. Erst Dev-Abnahme, dann gesonderte Produktionsfreigabe.

Ein Rückfall auf ältere App-Versionen kann die neuen Schutzregeln wieder
entfernen. Dabei keine neuen Zahlungen löschen und keine ältere Datenbank über
zwischenzeitliche Vorgänge zurückspielen. Die spätere R03-Indexänderung braucht
einen eigenen Übergangs-/Rollbackplan: alte Backfill-Abfragen dürfen nicht
ungeprüft gegen einen geänderten Unique-Index laufen.

## 5. Kundenportal

Ein Portal-Link ist ein GEHEIMNIS mit Ablaufdatum (Person → „Portal-Link"):
wer ihn hat, sieht Saldo, Rechnungen (mit Zahlungs-QR), Gefährte und
Übergabeprotokolle dieser Person — ohne Anmeldung. Deshalb:

- Links nur über den vorgesehenen Versand (oder persönlich) weitergeben, nie in
  öffentliche Kanäle.
- Kompromittiert? Person → Portal-Links → widerrufen. Wirkt sofort.
- Das Portal kann nichts ändern: Kundenwünsche (Kontaktdaten, Abholtermin)
  landen als Anfrage auf dem Dashboard und werden dort übernommen — oder nicht.
- Abgelaufene Links räumen sich selbst auf (stündlicher Sweep).
- Auch Verlängerung und „Kurz abholen“ (Ruhezeit) sind im Portal nur Anfragen,
  die du übernimmst. Einen Abholtermin wählen Kunden aus den freien
  Abholzeitfenstern, einen hinterlegten Einstellvertrag lesen und nehmen sie im
  Portal an (§10).

## 6. Nachweise und Aufbewahrung

- **Rechnungen sind unveränderlich** (Datenbank-Trigger, BAO §131/§132):
  Korrektur = Storno + neue Rechnung. Das Storno-Dokument nennt die stornierte
  Nummer.
- **Übergabeprotokolle sind unveränderlich**, sobald unterschrieben.
- **DSGVO-Löschung** (Person → anonymisieren): leert die Personenzeile,
  widerruft Portal-Links und entfernt Name+Unterschrift aus den
  Übergabeprotokollen — die Belege selbst bleiben (aufbewahrungspflichtig).
  Konten ausscheidender MITARBEITER dagegen: **sperren statt löschen**
  (Benutzerverwaltung → „Zugang sperren"), sonst verlieren Rechnungen und
  Zahlungen ihre Urheber-Zuordnung.
- **Datenauskunft (Art. 15 DSGVO):** Person → „Datenauskunft (ZIP)“ (nur
  Admins) packt alle Daten zur Person in ein Archiv, samt Rechnungs- und
  Übergabe-PDFs, Fotos, Anhängen und Mail-Protokoll. Der Abruf steht im
  Änderungsprotokoll.
- **Zustandsfotos** hängen am Übergabeprotokoll, lassen sich bis 24 Stunden
  danach ergänzen und sind einzeln nicht löschbar.
- **Angenommene Einstellverträge** liegen als PDF bei der Person, in genau der
  Version, die der Kunde gelesen hat.
- **Warteliste:** kein Eintrag ohne Einwilligung; Einträge werden nach
  zwölf Monaten gelöscht.
- **Revisionsexport** (Audit-Log): JSONL, jede Zeile durch eine SHA-256-Kette an
  alle vorigen gebunden. Das Manifest am Ende (Zeilenzahl + Endglied) getrennt
  ablegen; jeder spätere Export lässt sich dagegen prüfen — mit nichts als
  SHA-256, ohne Parkrr.

## 7. Überwachung (optional)

`/metrics` liefert Prometheus-Metriken (per `PARKRR_METRICS_TOKEN` schützen!).
Fertige Startpunkte liegen im Repo: `ops/prometheus-alerts.yml` (Erreichbarkeit,
5xx-Quote, p95-Latenz, DB-Pool, Neustart-Schleife) und
`ops/grafana-dashboard.json` (importierbar). Die Schwellwerte sind bewusst
unempfindlich — nachschärfen gehört zum Betrieb.

## 8. Wenn etwas klemmt

| Symptom | Erster Griff |
|---|---|
| Anwendung startet nicht | Container-Log: `slog fatal`-Zeile lesen. Häufig: fehlende Pflicht-Variable, unbekannte `PARKRR_TIMEZONE`, veränderte Migration (§3). |
| Alles langsam | Grafana/`parkrr_db_pool_*`: wartet die Anwendung auf DB-Verbindungen? Dann Langläufer in `pg_stat_activity` suchen, nicht den Pool vergrößern. |
| Viele 429 im Log | Der Per-IP-Limiter (`PARKRR_RATE_LIMIT_PER_MIN`, Default 600). Hinter einem Reverse-Proxy `PARKRR_TRUSTED_PROXY` + CIDRs setzen, sonst zählen alle Nutzer als eine IP. |
| Login klemmt trotz richtigem Passwort | Konto gesperrt? (Benutzerverwaltung). Lockout nach Fehlversuchen? 10 Minuten warten. `disabled`-Konten bekommen absichtlich dieselbe Meldung wie falsche Zugangsdaten. |
| Backup-Kachel rot | Änderungsprotokoll → Aktion „Backup fehlgeschlagen": dort steht die Stufe (Dump, Prüfung, Upload) samt Fehler. |
| Mail kommt nicht an | Audit-Log → „E-Mail-Versand": jeder Versuch mit SMTP-Diagnose. |
| Rechnung oder Mahnung ging nicht automatisch raus | Mail-Automatik eingeschaltet (§10)? Bei der Person „Nicht automatisch mahnen" gesetzt? Danach Audit-Log → „E-Mail-Versand". Eine Sendung mit „Zustellung unklar" wiederholt Parkrr absichtlich nicht von selbst, damit nichts doppelt ankommt; bei Bedarf von Hand erneut senden. |
| Startseite zeigt bei einer Halle keine Prozent | Die Halle war seit dem Update nicht im Planer offen. Einmal als Bearbeiter öffnen; der Planer trägt die nutzbare Fläche nach. |
| Ruhezeit lässt sich nicht eintragen (409) | Der Monat ist schon abgerechnet; die Meldung nennt den frühesten möglichen Tag. Eine Korrektur geht über Storno und Gutschrift. |
| Hallendienst sieht keine Beträge | Gewollt (§10, Rollen). |

## 9. Demo & Spielwiese

`parkrr seed-demo` befüllt eine FRISCHE Datenbank mit erkennbaren Demo-Daten
(Präfix „Demo:", idempotent). Auf einer Datenbank mit echten Personen verweigert
das Kommando den Dienst — absichtlich und ohne `--force`.

## 10. Die Funktionen aus dem Ausbau (Oktober 2026)

Je Bereich: was es tut, wo es liegt und was ausgeschaltet ausgeliefert wird. Alles
mit Wirkung nach außen ist aus, bis du es einschaltest.

### Rollen

Neben Administrator, Bearbeiter und Nur-Lesen gibt es zwei Rollen (Benutzer →
Rolle):

- **Buchhaltung** liest alles, dazu Steuerbereich und Exporte, und schreibt sonst
  nichts. Gedacht für Steuerberatung oder Buchhaltung.
- **Hallendienst** platziert Gefährte im Planer, macht Übergaben und Fotos,
  erledigt Serviceaufgaben, gibt Schlüssel aus und tippt bei Rückkehr aus einer
  Ruhezeit „Ist zurück“. Beträge sieht er nirgends; der Server schwärzt sie und
  lehnt Geld-Seiten ab. Seine Startseite ist „Heute in der Halle“. Den Grundriss
  ändern weiterhin nur Bearbeiter.

Die Rechte prüft der Server, nicht nur die Oberfläche: Jede Route ist für jede
Rolle getestet.

### Halle und Betrieb

- **Flächenauslastung:** Startseite, Planer-Kopfzeile und Kennzahlen zeigen
  dieselbe Zahl, belegte m² geteilt durch die nutzbare Fläche (ohne Wände,
  Fahrstraßen und Sperrflächen). Die Fläche rechnet der Planer und speichert sie
  mit dem Grundriss. Eine Halle aus der Zeit davor trägt sie beim ersten Öffnen
  durch einen Bearbeiter nach; bis dahin zeigt die Startseite nur die Zahl der
  Gefährte.
- **Platz vorschlagen** (Planer, beim bereiten Gefährt): drei gültige Plätze,
  bewertet nach Strom, Abholtermin und dichtem Packen. Steht der Platz vom
  letzten Mal noch leer in der Halle, kommt er zuerst; beim Saisonwechsel ist das
  der Platz des Vorjahres.
- **Zustandsfotos** gehören zur Übergabe, mit festen Ansichten und einer
  Schadenliste (siehe §6).
- **Serviceaufgaben** (Menü): fällig nach Intervall, je Gefährt, Tarif oder für
  alle mit Ladebedarf. „Erledigt“ kann eine Leistung als Zusatzkosten buchen.
- **Schlüsselbuch** (Menü): Schlüssel, Handsender und Karten mit Ausgabe und
  Rückgabe. Es speichert nie einen Code. Eine Person mit offenem Zugangsmittel
  lässt sich nicht löschen.
- **Wochenliste** (Kalender) und **Etikettenbogen** (je Halle) als PDF.
- **Kalender-Abo** (Einstellungen): Abholungen und Einlagerungen im eigenen
  Kalender, ohne Namen und Beträge. Die Adresse ist ein Geheimnis wie ein
  Portal-Link; gespeichert wird nur ihr Hash, widerrufen geht jederzeit.

### Saison und Kunden

- **Saisonwechsel** (Gefährte): stellt abgeholte Gefährte der letzten zwölf
  Monate in einem Zug neu ein, wahlweise zum aktuellen Tarif und als Reservierung
  ab dem neuen Beginn. Jede Kopie merkt sich ihren Vorgänger.
- **Warteliste** (Personen → Warteliste): Interessenten ohne freien Platz; nur
  mit Einwilligung, Löschung nach zwölf Monaten.
- **Abholzeitfenster** (Einstellungen): Wochenfenster, aus denen Kunden im Portal
  einen freien Zeitpunkt wählen. Ohne Fenster nennen Kunden wie bisher ein
  Wunschdatum.
- **Verlängerung und „Kurz abholen“** fragen Kunden im Portal an; beides landet
  als Kundenwunsch auf dem Dashboard (§5).

### Geld und Recht

- **Kontoauszug** (Menü): camt.053 einlesen. Es zählen nur gebuchte Gutschriften,
  ein Auszug wird nie doppelt eingelesen. Parkrr schlägt die Rechnung über die
  Nummer im Verwendungszweck vor, sonst über Betrag und Name; bei Mehrdeutigkeit
  keine. Gebucht wird erst beim Zuordnen.
- **Kautionen** (Personenseite): eigenes Konto. Erhaltene und zurückgezahlte
  Kautionen sind kein Erlös; ein Einbehalt braucht einen Grund. Die Übergabe bei
  der Abholung erinnert an eine offene Kaution.
- **Wertsicherung** (Tarife & Dienste → Wertsicherung (VPI)): VPI-Werte trägst du
  selbst ein, je Tarif Basismonat und Schwelle. Die Vorschau zeigt alt und neu je
  Gefährt; angepasst wird erst ab Beginn der nächsten Periode. Pauschalen bleiben
  fest. Die Mitteilung an Kunden per Mail ist eine eigene Wahl beim Anpassen.
- **Einstellvertrag** (Einstellungen, nur Admins): versionierte Vorlage mit
  Platzhaltern. Sobald eine Vorlage gespeichert ist, lesen und nehmen Kunden sie
  im Portal an.
- **Datenauskunft** nach Art. 15 DSGVO: siehe §6.

### Ruhezeit („Kurz weg“)

Ein eingelagertes Gefährt ist ein paar Wochen unterwegs (Urlaub, Messe) und kommt
zurück. Am Gefährt: „Kurz weg …“, danach „Ist zurück“, „+1 Woche“ oder „Doch
abgeholt“.

- Miete und gebundene Nebenkosten ruhen tagesgenau ab dem ersten Tag, ohne
  Haltegebühr. Unter einer Pauschale wird die Ruhezeit nur vermerkt.
- Eine offene Ruhezeit ruht weiter, bis jemand „Ist zurück“ tippt; nichts springt
  von selbst auf volle Miete zurück. Die Heute-Liste meldet fällige Rückkehren.
- Der Platz wird standardmäßig gehalten; freigeben geht erst am Abfahrtstag.
- Abgerechnete Monate sind gesperrt. Vorab Bezahltes verschiebt „bezahlt bis“
  um die Ruhetage.

### Mail-Automatik

Alle Mails laufen über eine Warteschlange. Fehlschläge versucht Parkrr nach
1 Minute, 5 und 30 Minuten, 2 und 6 Stunden erneut; den Verlauf zeigt Audit-Log →
„E-Mail-Versand“. Auch eine von Hand verschickte Rechnung läuft über die
Warteschlange; nur eine von Hand ausgelöste Zahlungserinnerung geht sofort.

Die Schalter liegen unter Rechnungs-Einstellungen → Mail-Automatik und sind aus:

| Schalter | Wirkung |
|---|---|
| Rechnungen aus dem automatischen Lauf per Mail senden | Nach dem Rechnungslauf geht jede neue Rechnung als PDF an die Person. Wirkt nur mit `PARKRR_AUTO_INVOICE_CRON`. |
| Automatisch mahnen | Zahlungserinnerung, 1. und 2. Mahnung nach den eingestellten Tagen. Eine Stufe zählt erst, wenn die Mail zugestellt ist. Personen lassen sich einzeln ausnehmen („Nicht automatisch mahnen“). |
| Erinnerung vor Vertragsende (Tage) | So viele Tage vor dem Ende eine Mail: verlängern oder Abholtermin vereinbaren. Je Gefährt und Ende genau einmal. 0 = aus. |

## 11. Einführung in zwei Schritten

Erst alles ohne Kundenkontakt, nach etwa zwei Wochen die Mail-Automatik. So
fällt eine falsche Einstellung auf, bevor ein Kunde eine falsche Mail bekommt.
Alles ist freiwillig; was du nicht nutzt, bleibt einfach aus.

**Schritt 1, ohne Kundenkontakt:**

1. Update eingespielt (§3), Backup-Kachel grün.
2. Bei Bedarf Konten für Buchhaltung und Hallendienst anlegen und mit dem
   Hallendienst-Konto einmal ausprobieren.
3. Jede Halle einmal als Bearbeiter im Planer öffnen (Flächenauslastung).
4. Serviceaufgaben anlegen, Schlüsselbuch füllen.
5. Einen Kontoauszug einlesen und die Vorschläge prüfen.
6. Wertsicherung: VPI-Werte eintragen und die Vorschau ansehen, noch nichts
   anpassen.
7. Ab und zu Audit-Log → „E-Mail-Versand“ ansehen: Nichts darf hängen bleiben.

**Schritt 2, nach etwa zwei Wochen** (SMTP eingerichtet, Verkäufer-Pflichtangaben
vollständig):

1. Die drei Mail-Schalter einzeln einschalten, mit ein paar Tagen Abstand, und
   danach jeweils den E-Mail-Versand ansehen. Vor dem automatischen Mahnen die
   Personen ausnehmen, die keine Mahnung bekommen sollen.
2. Einstellvertrag-Vorlage speichern.
3. Abholzeitfenster eintragen.
4. Die erste Wertsicherung anpassen, mit Mail an die Kunden nur, wenn gewollt.
