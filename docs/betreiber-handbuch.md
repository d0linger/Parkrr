# Parkrr — Betreiber-Handbuch

Für die Person, die Parkrr BETREIBT: installiert, sichert, aktualisiert und im
Ernstfall wiederherstellt. Die Bedienung der Anwendung selbst erklärt sich in der
Oberfläche; dieses Handbuch behandelt alles außenherum. (Hundert 97)

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

**Wiederherstellen:**

```bash
# Aus einer Archivdatei (destruktiv — überschreibt die Datenbank, atomar):
parkrr restore /backups/parkrr-2026-09-07-030000.dump.enc

# Aus S3: Backup-Reiter → „Aus S3 wiederherstellen" (verlangt den Schlüssel
# und das getippte Wort RESTORE).
```

Nach einer Wiederherstellung melden sich alle Benutzer neu an (Sitzungen leben
in der Datenbank).

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

## 9. Demo & Spielwiese

`parkrr seed-demo` befüllt eine FRISCHE Datenbank mit erkennbaren Demo-Daten
(Präfix „Demo:", idempotent). Auf einer Datenbank mit echten Personen verweigert
das Kommando den Dienst — absichtlich und ohne `--force`.
