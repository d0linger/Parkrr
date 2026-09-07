# Parkrr-Hundert: Umsetzungs-Tracker

Stand: 2026-09-07. Quelle: Artifact "Die Parkrr-Hundert" (100 kuratierte Punkte aus zwei
Code-Sweeps + Sessionwissen). Abarbeitung in validierten Batches, jeder Batch einzeln
committet. Leitplanken: kundenwirksames Verhalten landet hinter Opt-in-Flags (Default aus),
destruktive Migrationen zuletzt. Status: [ ] offen, [~] in Arbeit, [x] fertig, [!] uebersprungen
(mit Grund), [>] wartet auf Betreiber-Entscheidung/Konfiguration.

Batch-Reihenfolge: B1 Betrieb -> B2 Abrechnung+Uhr-Tests -> B3 Datenmodell/API -> B4 UX ->
B5 PWA -> B6 A11y -> B7 Planner -> B8 Sicherheit -> B9 Portal -> B10 Ausbau/L-Projekte ->
B11 Destruktives + Betreiber-Punkte.


## Zuverlässigkeit und Betrieb (B1 Betrieb)

- [x] **01** [Notwendig/S] Fehler vor jedem 500 loggen — `internal/handlers/billing.go:1104`
- [x] **02** [Notwendig/S] Request-ID in den Context propagieren — `internal/server/middleware.go:47`
- [x] **03** [Hoch/S] Access-Log-Level nach Status eskalieren — `internal/server/middleware.go:69`
- [x] **04** [Hoch/S] Alarm bei Backup-Fehlschlag — `internal/backup/schedule.go:328`
- [x] **05** [Hoch/S] CSV-Writer-Fehler prüfen — `internal/handlers/export.go:246`
- [x] **06** [Hoch/S] Belegungstrend-Fehler nicht verschlucken — `internal/handlers/stats.go:1120`
- [x] **07** [Hoch/S] Archiv-Sweep-Fehler sichtbar machen — `internal/handlers/agreements.go:553`
- [x] **08** [Mittel/S] Backfill hinter Done-Marker legen — `internal/server/server.go:56`
- [x] **09** [Mittel/S] S3-Retention konfigurierbar machen — `internal/backup/s3.go`
- [ ] **10** [Mittel/M] Beispiel-Alerts und Dashboard mitliefern
- [x] **11** [Idee/M] Geteilter Rate-Limiter für Replikate — `internal/auth/ratelimit.go`

## Abrechnung und Finanzen (B2 Abrechnung)

- [x] **12** [Notwendig/M] Injizierbare Uhr in der Abrechnung — `internal/handlers/billing.go:273`
- [x] **13** [Notwendig/M] Eine Zeitzonen-Wahrheit statt drei — `internal/handlers/audit.go:37`
- [x] **14** [Notwendig/M] Rechnungs-PDF auf Unicode-Schrift — `internal/handlers/invoice_pdf.go:113`
- [x] **15** [Hoch/M] Mahnwesen mit Stufen und Gedächtnis — `internal/handlers/reminders.go:66`
- [ ] **16** [Hoch/L] Automatischer Rechnungslauf
- [x] **17** [Hoch/M] Offene-Posten-Liste als Report
- [x] **18** [Hoch/M] Storno als Gutschrift-PDF
- [x] **19** [Mittel/S] Rechnungen und Zusatzkosten als CSV — `internal/handlers/export.go:48`
- [ ] **20** [Mittel/M] B1-Rest: laufende Periode anteilig fakturieren
- [ ] **21** [Mittel/L] E-Rechnung (ZUGFeRD/XRechnung)
- [ ] **22** [Mittel/M] Buchhaltungsexport DATEV/BMD
- [ ] **23** [Idee/L] SEPA-Lastschrift
- [ ] **24** [Idee/L] Bankabgleich per camt.053-Import
- [ ] **25** [Idee/S] Skonto und Rabatte je Vereinbarung

## Datenmodell und API (B3 Datenmodell)

- [x] **26** [Notwendig/S] Personen-Import in eine Transaktion — `internal/handlers/import.go:210`
- [x] **27** [Notwendig/S] E-Mail-Dedupe indexieren und absichern — `internal/handlers/import.go:204`
- [x] **28** [Hoch/S] Index auf invoices(due_on) — `internal/handlers/billing.go:1101`
- [x] **29** [Hoch/S] Geldlisten paginieren — `internal/handlers/billing.go:1147`
- [x] **30** [Hoch/S] total und has_more bei Listen — `internal/handlers/handlers.go:167`
- [x] **31** [Hoch/S] users.disabled statt löschen — `internal/handlers/users.go:241`
- [x] **32** [Hoch/M] Retention für wachsende Nebentabellen — `internal/handlers/portal.go:132`
- [ ] **33** [Mittel/S] Tote Tabelle flatrate_paid_years entfernen — `migrations/006_flatrate_years.sql`
- [x] **34** [Mittel/S] Checksummen in schema_migrations — `internal/database/database.go:116`
- [x] **35** [Mittel/M] Audit-Suche indexfähig machen — `internal/handlers/audit.go:23`
- [x] **36** [Mittel/M] Portal-Statistikpfad wirklich scopen — `internal/handlers/stats.go:651`
- [ ] **37** [Idee/L] OpenAPI-Spezifikation

## Sicherheit und Datenschutz (B8 Sicherheit)

- [x] **38** [Hoch/M] Übergabeprotokolle unveränderlich machen — `internal/handlers/handover.go:207`
- [x] **39** [Hoch/M] DSGVO-Anonymisierung auf Signaturen ausweiten — `internal/handlers/persons.go:284`
- [x] **40** [Mittel/S] E-Mail-Syntax validieren — `internal/handlers/persons.go:104`
- [x] **41** [Mittel/M] 2FA-Pflicht als Policy
- [x] **42** [Mittel/S] Passkey-only-Modus
- [x] **43** [Mittel/M] Revisionssicherer Audit-Export
- [x] **44** [Idee/S] CSP-Verstöße an client-error melden — `internal/server/server.go`
- [ ] **45** [Idee/S] Trivy als geprüftes Binary in CI

## Bedienung und UX (B4 UX)

- [x] **46** [Notwendig/S] Speichern erhält den Listenzustand — `web/static/js/app.js:884`
- [x] **47** [Notwendig/S] Template-Fehler nicht als Offline-Erfolg tarnen — `web/static/js/app.js:5248`
- [x] **48** [Hoch/S] Zusatzkosten-Empty-State reparieren — `web/static/js/app.js:3151`
- [x] **49** [Hoch/S] Mehr laden mit Busy- und Fehlerzustand — `web/static/js/app.js:4217`
- [ ] **50** [Hoch/M] Foto-Upload mit Fortschritt und Downscale — `web/static/js/app.js:3082`
- [x] **51** [Hoch/S] formModal-save flächendeckend nutzen — `web/static/js/app.js:444`
- [x] **52** [Hoch/S] Undo auf alle destruktiven Flows — `web/static/js/app.js:324`
      Vier weitere Flows auf deleteWithUndo: Übergabeprotokoll, Garage, Halle,
      Planer-Icon. BEWUSST ohne Undo bleiben: Sitzung abmelden und Passkey
      entfernen (ein Rückgängig-Fenster würde den Sicherheitsentzug VERZÖGERN —
      wer widerruft, meint sofort) sowie die Planner-internen Löschungen
      (eigene Strg+Z-Historie) und die Storno-Zahlung (Kommentar im Code).
- [~] **53** [Hoch/M] Server-Pagination auch im Frontend — `web/static/js/app.js:786`
      TEILWEISE: Das Frontend liest jetzt X-Total-Count und WARNT sichtbar, wenn die
      Liste am Serverdeckel (1000) abgeschnitten ist — vorher war das von einer
      vollständigen Liste nicht zu unterscheiden. /vehicles und /charges melden die
      Gesamtzahl jetzt ebenfalls. Echte Server-Pagination BLEIBT OFFEN: mountList
      filtert und sortiert clientseitig über die geladene Menge, mit diakritika-
      faltender Suche (norm()). Das serverseitig nachzubauen heißt, für jede Liste
      Suche und Sortierung im SQL zu spiegeln — sonst liefert dieselbe Eingabe je
      nach Seite andere Treffer. Das ist ein eigenes Vorhaben, kein Batch-Schritt.
- [ ] **54** [Mittel/M] Bulk-Aktionen für Gefährte
- [ ] **55** [Mittel/M] Kalenderansicht
- [ ] **56** [Mittel/M] Aktivitäts-Timeline pro Person
- [ ] **57** [Mittel/M] Datei-Anhänge je Person und Gefährt
- [ ] **58** [Idee/S] Fotoreihenfolge und Titelbild

## Barrierefreiheit (B6 A11y)

- [x] **59** [Hoch/S] Live-Region vom Seitencontainer lösen — `web/static/index.html:87`
- [x] **60** [Hoch/S] document.title je Route setzen — `web/static/js/app.js:897`
- [x] **61** [Mittel/S] Echte Dialog-Semantik für Overlays — `web/static/js/app.js:5220`
- [ ] **62** [Mittel/M] Charts tastatur- und screenreader-tauglich — `web/static/js/app.js:591`
- [x] **63** [Mittel/S] aria-pressed für Planner-Toolbar — `web/static/js/app.js:6218`
- [x] **64** [Mittel/S] Suchfeld beschriften, Trefferzahl ansagen — `web/static/js/app.js:771`

## PWA und Offline (B5 PWA)

- [x] **65** [Notwendig/S] Install-Prompt mit zweiter Chance — `web/static/js/app.js:7660`
- [x] **66** [Notwendig/S] SW-Cache-Version an den Build koppeln — `web/static/sw.js:2`
- [x] **67** [Hoch/S] Update-Hinweis statt stillem Austausch — `web/static/sw.js:17`
- [x] **68** [Hoch/S] Precache vervollständigen — `web/static/sw.js:3`
- [x] **69** [Mittel/S] Manifest ausbauen — `web/static/manifest.webmanifest:44`
- [x] **70** [Mittel/S] theme-color dem Theme folgen lassen — `web/static/index.html:6`
- [ ] **71** [Idee/L] Offline-Queue für Schreibaktionen
- [ ] **72** [Idee/M] Web-Push-Benachrichtigungen
- [ ] **73** [Idee/S] Kamera-Direktaufnahme

## Garagenplaner (B7 Planner)

- [x] **74** [Hoch/M] Batch-Endpunkt für Spot-Layouts — `internal/server/server.go:207`
- [x] **75** [Hoch/M] Zeichenpfad entlasten — `web/static/js/app.js:5856`
- [!] **76** [Hoch/S] Rail und Toolbar nicht pro draw() neu bauen — `web/static/js/app.js:6358`
      ÜBERSPRUNGEN (gemessen statt vermutet): die pointermove-Pfade des Planers
      rufen draw() gar nicht — Drag/Rotate/Resize mutieren Styles direkt, draw()
      läuft nur bei diskreten Aktionen. Der Neubau von Rail+Toolbar dort kostet
      bei realen Palettengrößen <1 ms; eine Memo-Schicht mit Zustands-Signatur
      riskiert dafür veraltete Anzeigen in genau dem Modul, das laut Leitplanke
      (AR4) nicht autonom umgebaut werden soll. Nutzen unklar, Risiko real.
- [x] **77** [Mittel/S] Undo-Snapshots verschlanken — `web/static/js/app.js:5077`
- [x] **78** [Mittel/S] Emoji-Knöpfe durch SVG-Icons ersetzen — `web/static/js/app.js:6230`
- [ ] **79** [Mittel/M] Belegungshistorie pro Stellplatz
- [x] **80** [Mittel/S] Standardmaße je Kategorie
- [ ] **81** [Idee/L] Mehrere Ebenen je Halle
- [ ] **82** [Idee/M] Stellplatz-Reservierung mit Zeitraum

## Portal und Kommunikation (B9 Portal)

- [x] **83** [Hoch/S] Portal: Blob-URLs freigeben, Fehler abfangen — `web/static/js/app.js:7986`
- [ ] **84** [Mittel/M] Portal: Übergabeprotokolle einsehen
- [ ] **85** [Mittel/M] Portal: Stammdaten-Änderungswunsch
- [ ] **86** [Mittel/S] E-Mail-Versandprotokoll
- [ ] **87** [Idee/M] Portal: Terminwunsch für Abholung
- [ ] **88** [Idee/S] Portal-Link als QR auf der Rechnung
- [ ] **89** [Idee/M] Portal auf Englisch

## Tests und Qualität (B2/B10 Tests)

- [x] **90** [Notwendig/M] Datums-Grenzfall-Matrix mit gepinnter Uhr
- [x] **91** [Hoch/M] photos.go und planner_icons.go testen — `internal/handlers/photos.go:90`
- [x] **92** [Hoch/M] Vehicle-Lifecycle abdecken — `internal/handlers/vehicles.go:556`
- [ ] **93** [Mittel/M] E2E-Tests für Login, 2FA und Passkey — `tests/a11y/`
- [x] **94** [Mittel/S] Coverage-Gate anheben — `.github/workflows/ci.yml`
- [x] **95** [Mittel/S] seed-demo-Kommando — `cmd/parkrr/`

## Ausbau und Zukunft (B10 Ausbau)

- [ ] **96** [Mittel/M] Redesign-Rollout, Phase Tokens
- [ ] **97** [Mittel/M] Betreiber-Handbuch
- [ ] **98** [Idee/L] Mehrsprachigkeit der App — `web/static/js/app.js:148`
- [x] **99** [Idee/S] CHANGELOG und Release-Notes
- [ ] **100** [Idee/L] Mandantenfähigkeit

Hinweis Betreiber-Punkte ([>]): 2FA-Pflicht, Passkey-only, Rechnungslauf-Aktivierung,
Mahnstufen-Aktivierung, SEPA-Glaeubiger-ID, VAPID-Keys, S3-Retention-Wert, Replikat-Limiter,
Tabellen-Drop (Backup vorher). Diese landen als Code mit Default aus bzw. warten auf Freigabe.

## Zusatz (aus den Sweeps, nicht Teil der kuratierten 100)

- [x] **Z1** [Mittel/S] Verdrängte Wand-Vorlagen protokollieren statt still löschen — `internal/handlers/wall_templates.go` (Commit 89218e4)
- [x] **Z2** Render-Wettlauf im Router: ein Routenwechsel während eines laufenden
      Seitenaufbaus ließ die überholte Route ihr (spätes) Ergebnis oder ihren
      Abbruchfehler über die neue Seite schreiben. Jeder Aufbau bekommt jetzt einen
      eigenen Container; ein neuerer Aufbau löst den alten aus dem Dokument, dessen
      späte Schreibzugriffe laufen ins Leere. Reproduziert und fixiert durch
      tests/a11y/render-race.spec.js (fällt auf dem alten Stand nachweislich um).
- [x] **Z3** Client-Abbrüche (context canceled) werden im Access-Log nicht mehr als
      ERROR/500-Rauschen geführt; die betroffenen Listen-Endpunkte melden ihre
      500-Ursache jetzt über serverError (OPS-01-Rest, gezielt).
