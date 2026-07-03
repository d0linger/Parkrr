# Parkrr

Parkrr ist eine mobil-optimierte Web-Anwendung (PWA) zur Verwaltung von
**Einstellplätzen**: Gefährte (Auto, Anhänger, Wohnwagen, Wohnmobil …),
die Personen einstellen, sowie das laufende **Kostentracking** je Gefährt
auf monatlicher oder jährlicher Basis.

Geschrieben in **Go**, mit **PostgreSQL** als Datenbank, komplett über
**Docker** betreibbar. Alle Frontend-Assets (CSS, JS, Icons) werden lokal
ausgeliefert – keine externen CDNs.

---

## Funktionen

- **Personenverwaltung** – Kunden anlegen, bearbeiten, löschen, mit
  Detailseite (Deep-Link) inkl. Saldo, Diagrammen und Buchungen.
- **Gefährteverwaltung** – mehrere Gefährte pro Person; schlankes Formular
  (nur Stammdaten), **Detailseite** mit Fotos, Status und Verlauf.
- **Schieberegler-Bedienung** – Lager- und Zahlstatus je Gefährt direkt per
  Slider *reserviert · eingelagert · abgeholt* bzw. *offen · bezahlt* – ein
  Tipp, ohne Formular, direkt auf der Karte.
- **Saisonales Wiederverwenden** – Button *„↻ Erneut einstellen"* dupliziert
  ein abgeholtes Gefährt (Typ, Kennzeichen, Preis **und Fotos**) mit neuem
  Einstelldatum; für Kunden, die ihr Gefährt jedes Jahr einlagern.
- **Lebenszyklus & Reservierungen** – Status *reserviert → eingelagert →
  abgeholt → storniert* inkl. **Statusverlauf** (wer/wann/Notiz).
  Beim Abholen wird das Enddatum automatisch gesetzt.
- **Fotos** – pro Gefährt Bilder hochladen (JPEG/PNG/WebP, in der DB
  gespeichert), Galerie mit Lightbox.
- **Zentrale Tarife** – Gefährt-Typen mit Standardpreisen (Monat/Jahr),
  zentral verwaltbar, **pro Gefährt überschreibbar** (Sonderpreis).
- **Kostentracking** – tagesgenaue Kosten ab Einstell- bis Abholdatum (oder
  bis heute).
- **Zahlstatus per Slider** – „offen/bezahlt" wird direkt am Gefährt umgeschaltet
  (kein separates Zahlungs-Ledger). Offener Saldo = aufgelaufene Miete +
  Zusatzkosten − (als bezahlt markierte Gefährte inkl. ihrer Zusatzkosten).
- **Zusatzkosten** – Zusatzleistungen (Strom, Reinigung, Winterservice …)
  als Positionen; zentraler **Dienste-Katalog** mit Standardpreisen.
- **Statistiken & Diagramme** – Umsatz pro Monat, Zusatzkosten, Status­verteilung,
  bezahlt/offen, Kosten pro Person nach Monat und Jahr (lokale SVG-Charts, kein CDN).
- **Suche, Sortierung & Pagination** in allen Listen.
- **Audit-Log** – jede Änderung wird protokolliert (Admin-Ansicht).
- **Multi-User & Rollen** – Rollen *Admin, Standortleiter, Buchhaltung,
  Nur-Lesen*. Der **Admin** wird per Docker-ENV definiert und legt weitere
  Benutzer an. Siehe [Rollen & Rechte](#rollen--rechte).
- **Sicherheit** – **2FA (TOTP)** mit QR-Enrollment + **Backup-Codes**,
  TOTP-Secret **verschlüsselt** gespeichert (AES-GCM); Session-Tokens
  **gehasht** (SHA-256) in der DB; Login-Rate-Limiting + genereller per-IP-
  Throttle; **Sitzungsverwaltung** (aktive Geräte, einzeln/„überall" abmelden);
  gehärtete HTTP-Header (CSP, HSTS hinter TLS, Permissions-Policy);
  Foto-Uploads werden validiert & neu kodiert (EXIF/GPS entfernt).
- **Komfort** – manueller **Hell/Dunkel-Umschalter** (gespeichert),
  **Rückgängig** nach dem Löschen, moderne `<dialog>`-Modals.
- **PWA** – installierbar am Handy, offline-fähige App-Shell, mobil optimiert.

---

## Schnellstart (Docker)

Voraussetzungen: Docker + Docker Compose.

```bash
# 1. Konfiguration vorbereiten
cp .env.example .env
#   in .env mindestens setzen:
#   - PARKRR_ADMIN_PASSWORD
#   - PARKRR_SESSION_SECRET  (z. B. `openssl rand -base64 48`)
#   - PARKRR_DB_PASSWORD

# 2. Starten
docker compose up -d --build

# 3. Öffnen
#   http://localhost:8080
#   Login mit PARKRR_ADMIN_USERNAME / PARKRR_ADMIN_PASSWORD
```

Die Datenbank läuft als eigenständiger Postgres-Container und ist nur
innerhalb des Compose-Netzwerks erreichbar (kein Host-Port).
Datenbankschema-Migrationen laufen automatisch beim Start.

---

## Konfiguration (Umgebungsvariablen)

| Variable | Beschreibung | Default |
| --- | --- | --- |
| `PARKRR_LISTEN_ADDR` | HTTP-Listen-Adresse | `:8080` |
| `PARKRR_HTTP_PORT` | veröffentlichter Host-Port (compose) | `8080` |
| `PARKRR_DATABASE_URL` | vollständige Postgres-URL (überschreibt Einzelwerte) | – |
| `PARKRR_DB_HOST` / `PARKRR_DB_PORT` | DB-Host / -Port | `db` / `5432` |
| `PARKRR_DB_USER` / `PARKRR_DB_PASSWORD` / `PARKRR_DB_NAME` | DB-Zugang | `parkrr` |
| `PARKRR_DB_SSLMODE` | Postgres SSL-Mode | `disable` |
| `PARKRR_ADMIN_USERNAME` | Admin-Benutzername | `admin` |
| `PARKRR_ADMIN_EMAIL` | Admin-E-Mail | `admin@example.com` |
| `PARKRR_ADMIN_PASSWORD` | **Pflicht** – Admin-Passwort | – |
| `PARKRR_SESSION_SECRET` | **Pflicht** – Session-Secret (min. 16 Zeichen) | – |
| `PARKRR_SESSION_MAX_AGE` | Session-Dauer in Sekunden | `604800` (7 Tage) |
| `PARKRR_SECURE_COOKIES` | `Secure`-Flag für Cookies (bei HTTPS `true`) | `false` |
| `PARKRR_TRUSTED_PROXY` | hinter Reverse Proxy: `X-Forwarded-*` vertrauen | `false` |
| `PARKRR_RATE_LIMIT_PER_MIN` | genereller per-IP-Request-Budget/Minute (`0` = aus) | `600` |
| `PARKRR_LOG_FORMAT` / `PARKRR_LOG_LEVEL` | `json`\|`text` / `debug`..`error` | `json` / `info` |

> Der Admin-Account wird bei **jedem Start** aus den ENV-Werten erstellt bzw.
> aktualisiert (Passwort, E-Mail, Admin-Flag). Die ENV bleibt die
> maßgebliche Quelle für den Admin.

---

## Betrieb hinter einem Reverse Proxy (Nginx Proxy Manager, Traefik, Caddy …)

Parkrr nutzt ausschließlich **relative Pfade** und lauscht auf `:8080` – es lässt
sich ohne Anpassung hinter einem Reverse Proxy auf einer eigenen (Sub-)Domain
betreiben. TLS wird am Proxy terminiert.

**1. `.env` anpassen:**

```env
PARKRR_TRUSTED_PROXY=true      # X-Forwarded-For/-Proto vertrauen (nur hinter Proxy!)
PARKRR_SECURE_COOKIES=true     # optional; wird bei X-Forwarded-Proto=https automatisch gesetzt
# Optional: keinen Host-Port veröffentlichen und stattdessen nur im Docker-Netz
# erreichbar machen, wenn Proxy im selben Netzwerk läuft.
```

Mit `PARKRR_TRUSTED_PROXY=true` verwendet Parkrr die echte Client-IP aus
`X-Forwarded-For`/`X-Real-IP` (für Logs, Rate-Limiting, Audit) und erkennt
HTTPS über `X-Forwarded-Proto` (→ `Secure`-Cookies + HSTS). **Ohne** Proxy
sollte der Wert `false` bleiben, um Header-Spoofing zu verhindern.

**2. Nginx Proxy Manager – Proxy Host anlegen:**

- **Scheme:** `http`
- **Forward Hostname/IP:** Container-Name/Host von Parkrr (z. B. `parkrr-app`
  wenn NPM im selben Docker-Netz läuft, sonst die Host-IP)
- **Forward Port:** `8080` (Container-Port) bzw. der veröffentlichte Host-Port
- **Websockets Support:** nicht nötig
- Reiter **SSL:** Zertifikat (Let's Encrypt) auswählen, *Force SSL* + *HTTP/2*
  aktivieren. NPM setzt `X-Forwarded-Proto`/`X-Forwarded-For` automatisch.

Damit NPM den Container direkt erreicht, beide in dasselbe Docker-Netz hängen –
dann ist kein veröffentlichter Host-Port nötig (in `docker-compose.yml` den
`ports:`-Block der `app` entfernen und das externe NPM-Netz einbinden).

**3. Generisches Nginx-Beispiel:**

```nginx
server {
    listen 443 ssl http2;
    server_name parkrr.example.com;
    # ssl_certificate ...;  ssl_certificate_key ...;

    client_max_body_size 10m;   # Foto-Uploads (bis 8 MB)

    location / {
        proxy_pass         http://parkrr-app:8080;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;
    }
}
```

---

## Rollen & Rechte

| Rolle | Lesen | Personen & Gefährte | Zusatzkosten | Tarife, Dienste, Benutzer, Audit |
| --- | :---: | :---: | :---: | :---: |
| **Admin** | ✓ | ✓ | ✓ | ✓ |
| **Standortleiter** (`manager`) | ✓ | ✓ | ✓ | – |
| **Buchhaltung** (`accounting`) | ✓ | – | ✓ | – |
| **Nur-Lesen** (`readonly`) | ✓ | – | – | – |

Neue Benutzer erhalten standardmäßig die Rolle *Standortleiter*. Der letzte
Admin kann nicht herabgestuft oder gelöscht werden.

## Architektur

```
cmd/parkrr/            – Einstiegspunkt, Admin-Bootstrap, Server-Lifecycle
internal/config/       – Konfiguration aus ENV
internal/database/     – pgx-Pool + eingebettete SQL-Migrationen (001–004)
internal/models/       – Domänentypen + Kostenberechnung (mit Tests)
internal/auth/         – bcrypt, Sessions/CSRF, Rollen, 2FA (TOTP), Rate-Limit
internal/handlers/     – JSON-API (Auth/2FA, Personen, Gefährte, Fotos, Tarife,
                         Dienste, Zusatzkosten, Stats, Users, Audit)
internal/server/       – Routing, Middleware (Access-Log, Rate-Limit,
                         Security-Header), rollenbasierte Autorisierung
web/static/            – PWA-Frontend (SPA, SVG-Charts, Service Worker, Icons)
```

- **Backend:** Go-Standardbibliothek (`net/http` mit method-based Routing),
  Laufzeitabhängigkeiten: `pgx`, `golang.org/x/crypto`, `pquerna/otp` (2FA).
- **Frontend:** Vanilla JS Single-Page-App mit Hash-Routing, native
  `<dialog>`-Modals, SVG-Diagramme, moderne CSS (Custom Properties),
  Hell/Dunkel automatisch **oder** manuell umschaltbar.
- **Sicherheit:** bcrypt-Passwörter, HttpOnly-Session-Cookies mit **gehashten
  Tokens** in der DB, CSRF-Token (Double-Submit), rollenbasierte Autorisierung,
  2FA (TOTP, verschlüsselt) + Backup-Codes, Login- **und** per-IP-Rate-Limiting,
  strukturierte Logs (slog) mit Request-ID, gehärtete Header (CSP, HSTS,
  Permissions-Policy), Reverse-Proxy-fähig (`X-Forwarded-*`),
  Nicht-Root-Container (distroless).

### Kostenberechnung

Für jedes Gefährt gilt ein **effektiver Preis** = Sonderpreis, sonst der
zentrale Tarifpreis für die gewählte Abrechnungsart (monatlich/jährlich).
Die aufgelaufenen Kosten werden tagesgenau vom Einstelldatum bis zum
Abholdatum (oder bis heute) proratiert:

- monatlich: `Preis × Tage / (365,25 / 12)`
- jährlich: `Preis × Tage / 365,25`

---

## Entwicklung (ohne Docker)

Voraussetzungen: Go 1.23+, ein laufender PostgreSQL.

```bash
export PARKRR_DATABASE_URL="postgres://parkrr:parkrr@localhost:5432/parkrr?sslmode=disable"
export PARKRR_ADMIN_PASSWORD="dev-admin-pw"
export PARKRR_SESSION_SECRET="dev-session-secret-please-change"

go mod tidy
go run ./cmd/parkrr
# http://localhost:8080
```

Tests & Qualität:

```bash
go test ./...
go vet ./...
golangci-lint run          # Linting
gosec ./...                # Security
govulncheck ./...          # Vulnerabilities
```

---

## CI / CD

GitHub Actions Workflows unter `.github/workflows/`:

| Workflow | Zweck |
| --- | --- |
| `ci.yml` | Build, `go vet`, Tests (mit Race-Detector), Docker-Build |
| `golangci-lint.yml` | Statische Analyse / Linting |
| `gosec.yml` | Security-Scanner |
| `govulncheck.yml` | Abgleich bekannter Schwachstellen (auch wöchentlich) |
| `dependency-review.yml` | Dependency-Review + Modul-Graph/Updates |

**Dependabot** (`.github/dependabot.yml`) hält Go-Module, GitHub-Actions und
Docker-Basis-Images aktuell.

---

## Lizenz

MIT – siehe [LICENSE](LICENSE). Es werden ausschließlich freie, lizenzkosten-
freie Werkzeuge und Bibliotheken verwendet.
