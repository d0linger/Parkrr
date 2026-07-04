<div align="center">

<img src="web/static/icons/icon.svg" width="104" alt="Parkrr logo">

# Parkrr

**Selbst gehostete Verwaltung für Einstellplätze** – Gefährte, Personen und Kosten
als mobil-optimierte PWA. In Go geschrieben, mit PostgreSQL, komplett per Docker.

[![CI](https://github.com/preining/parkrr/actions/workflows/ci.yml/badge.svg)](https://github.com/preining/parkrr/actions/workflows/ci.yml)
[![golangci-lint](https://github.com/preining/parkrr/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/preining/parkrr/actions/workflows/golangci-lint.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-14b8a6.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![PWA](https://img.shields.io/badge/PWA-installierbar-0d9488)
![Self-hosted](https://img.shields.io/badge/self--hosted-Docker-2496ED?logo=docker&logoColor=white)

</div>

Parkrr hilft kleinen Betrieben (Stellplatz-Vermietung, Winterlager, Camping- und
Bootslager …), **Gefährte** (Auto, Anhänger, Wohnwagen, Wohnmobil, Motorrad …)
zu verwalten, die **Personen** einstellen, und die **laufenden Kosten** je Gefährt
auf monatlicher oder jährlicher Basis tagesgenau zu berechnen.

> Alle Frontend-Assets (CSS, JS, Icons, Diagramme) werden **lokal** ausgeliefert –
> keine externen CDNs, kein Tracking, keine Cloud. Deine Daten bleiben bei dir.

---

## 📸 Screenshots

| Login | Übersicht | Gefährte | Person |
| --- | --- | --- | --- |
| ![Login](docs/screenshots/01-login.png) | ![Übersicht](docs/screenshots/02-dashboard.png) | ![Gefährte](docs/screenshots/03-vehicles.png) | ![Person](docs/screenshots/04-person.png) |

---

## ✨ Funktionen

- **Personen & Gefährte** – mehrere Gefährte pro Person, schlankes Formular,
  Detailseiten (Deep-Links) mit Saldo, Diagrammen und Verlauf.
- **Schieberegler-Bedienung** – Lager- und Zahlstatus je Gefährt direkt per Slider
  (*reserviert · eingelagert · abgeholt* bzw. *offen · bezahlt*), ein Tipp, ohne Formular.
- **Saisonales Wiederverwenden** – *„↻ Erneut einstellen"* dupliziert ein abgeholtes
  Gefährt (Typ, Kennzeichen, Preis **und Fotos**) mit neuem Einstelldatum.
- **Lebenszyklus & Reservierungen** – Statusverlauf (wer/wann/Notiz); beim Abholen
  wird das Enddatum automatisch gesetzt.
- **Fotos** – pro Gefährt (JPEG/PNG), beim Upload validiert und neu kodiert
  (EXIF/GPS entfernt), Galerie mit Lightbox.
- **Zentrale Tarife** – Gefährt-Typen mit Standardpreisen, **pro Gefährt
  überschreibbar** (Sonderpreis).
- **Kostentracking** – tagesgenaue Kosten ab Einstell- bis Abholdatum (oder bis heute).
- **Zusatzkosten** – Strom, Reinigung, Winterservice … aus einem Dienste-Katalog.
- **Statistiken & Diagramme** – Umsatz/Monat, Status­verteilung, bezahlt/offen,
  Kosten pro Person nach Monat und Jahr (lokale SVG-Charts).
- **Multi-User & Rollen** – *Admin, Standortleiter, Buchhaltung, Nur-Lesen*.
- **Sicherheit** – 2FA (TOTP) + Backup-Codes, gehashte Session-Tokens,
  Rate-Limiting, gehärtete HTTP-Header. Siehe [Sicherheit](#-sicherheit).
- **PWA** – installierbar am Handy, Hell/Dunkel, offline-fähige App-Shell.

---

## 🚀 Schnellstart (Docker)

Voraussetzungen: Docker + Docker Compose.

```bash
git clone https://github.com/preining/parkrr.git
cd parkrr

# 1. Konfiguration
cp .env.example .env
#   in .env mindestens setzen:
#   - PARKRR_ADMIN_PASSWORD
#   - PARKRR_SESSION_SECRET   (z. B.:  openssl rand -base64 48)
#   - PARKRR_DB_PASSWORD

# 2. Starten
docker compose up -d --build

# 3. Öffnen:  http://localhost:8080
#    Login mit PARKRR_ADMIN_USERNAME / PARKRR_ADMIN_PASSWORD
```

Die Datenbank läuft als eigenständiger Postgres-Container (nur im Compose-Netz
erreichbar). Schema-Migrationen laufen automatisch beim Start.

---

## ⚙️ Konfiguration

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
> aktualisiert – die ENV bleibt die maßgebliche Quelle für den Admin.

---

## 🌐 Betrieb hinter einem Reverse Proxy (Nginx Proxy Manager, Traefik, Caddy …)

Parkrr nutzt ausschließlich **relative Pfade** und lauscht auf `:8080` – es lässt
sich ohne Anpassung hinter einem Reverse Proxy auf einer eigenen (Sub-)Domain
betreiben. TLS wird am Proxy terminiert.

**1. `.env` anpassen:**

```env
PARKRR_TRUSTED_PROXY=true      # X-Forwarded-For/-Proto vertrauen (nur hinter Proxy!)
PARKRR_SECURE_COOKIES=true     # optional; wird bei X-Forwarded-Proto=https automatisch gesetzt
```

Mit `PARKRR_TRUSTED_PROXY=true` verwendet Parkrr die echte Client-IP aus
`X-Forwarded-For`/`X-Real-IP` (für Logs, Rate-Limiting, Audit) und erkennt HTTPS
über `X-Forwarded-Proto` (→ `Secure`-Cookies + HSTS). **Ohne** Proxy sollte der
Wert `false` bleiben, um Header-Spoofing zu verhindern.

**2. Nginx Proxy Manager – Proxy Host:**

- **Scheme** `http` · **Forward Hostname** `parkrr-app` (bei gemeinsamem Docker-Netz)
  bzw. Host-IP · **Forward Port** `8080` · **Websockets** nicht nötig
- Reiter **SSL:** Let's-Encrypt-Zertifikat, *Force SSL* + *HTTP/2* aktivieren.
  NPM setzt `X-Forwarded-Proto`/`X-Forwarded-For` automatisch.

Für den direkten Zugriff im Docker-Netz beide Container in dasselbe Netz hängen
und den `ports:`-Block der `app` in `docker-compose.yml` entfernen.

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

## 👥 Rollen & Rechte

| Rolle | Lesen | Personen & Gefährte | Zusatzkosten | Tarife, Dienste, Benutzer, Audit |
| --- | :---: | :---: | :---: | :---: |
| **Admin** | ✓ | ✓ | ✓ | ✓ |
| **Standortleiter** (`manager`) | ✓ | ✓ | ✓ | – |
| **Buchhaltung** (`accounting`) | ✓ | – | ✓ | – |
| **Nur-Lesen** (`readonly`) | ✓ | – | – | – |

Neue Benutzer erhalten standardmäßig die Rolle *Standortleiter*. Der letzte Admin
kann nicht herabgestuft oder gelöscht werden.

---

## 🔒 Sicherheit

- **Passwörter** mit bcrypt, **Session-Tokens gehasht** (SHA-256) in der DB.
- **CSRF** via Double-Submit-Token, **rollenbasierte** Autorisierung.
- **2FA (TOTP)** mit QR-Enrollment und **einmaligen Backup-Codes**; das
  TOTP-Secret wird **verschlüsselt** (AES-GCM) gespeichert.
- **Rate-Limiting**: Login-Lockout nach zu vielen Fehlversuchen **plus**
  genereller per-IP-Throttle.
- **Foto-Uploads** werden dekodiert und neu kodiert (**EXIF/GPS entfernt**),
  nur echtes JPEG/PNG, mit Dimensions- und Anzahllimit.
- Gehärtete HTTP-Header: **CSP**, **HSTS** (hinter TLS), `Permissions-Policy`,
  COOP/CORP; Session-Cookies `HttpOnly` + `SameSite`.
- **Sitzungsverwaltung**: aktive Geräte anzeigen, einzeln oder „überall" abmelden.
- **Audit-Log** jeder Änderung; strukturierte Logs (slog) mit Request-ID.
- Läuft als **Nicht-Root** in einem `distroless`-Container.

Sicherheitslücken bitte **nicht** über öffentliche Issues melden – siehe
[SECURITY.md](SECURITY.md).

---

## 🧮 Kostenberechnung

Effektiver Preis = Sonderpreis, sonst zentraler Tarifpreis für die gewählte
Abrechnungsart. Aufgelaufene Kosten werden tagesgenau vom Einstell- bis zum
Abholdatum (oder bis heute) proratiert:

- monatlich: `Preis × Tage / (365,25 / 12)`
- jährlich: `Preis × Tage / 365,25`

„Bezahlt" ergibt sich aus dem **Zahl-Slider** je Gefährt; offener Saldo =
aufgelaufene Miete + Zusatzkosten − (als bezahlt markierte Gefährte).

---

## 🏗️ Architektur

```
cmd/parkrr/        – Einstiegspunkt, Admin-Bootstrap, Logging, Server-Lifecycle
internal/config/   – Konfiguration aus ENV
internal/database/ – pgx-Pool + eingebettete SQL-Migrationen (001–004)
internal/models/   – Domänentypen + Kostenberechnung (mit Tests)
internal/auth/     – bcrypt, Sessions/CSRF, Rollen, 2FA (TOTP/AES-GCM), Rate-Limit
internal/handlers/ – JSON-API (Auth/2FA, Personen, Gefährte, Fotos, Tarife,
                     Dienste, Zusatzkosten, Stats, Users, Audit)
internal/server/   – Routing, Middleware (Access-Log, Rate-Limit, Security-Header)
web/static/        – PWA-Frontend (SPA, SVG-Charts, Service Worker, Icons)
```

- **Backend:** Go-Standardbibliothek (`net/http`, method-based Routing);
  Laufzeit-Abhängigkeiten nur `pgx`, `golang.org/x/crypto`, `pquerna/otp`.
- **Frontend:** Vanilla-JS-SPA mit Hash-Routing, native `<dialog>`-Modals,
  SVG-Diagramme, moderne CSS – kein Framework, kein Build-Step.

---

## 🧑‍💻 Entwicklung (ohne Docker)

Voraussetzungen: Go 1.25+, ein laufender PostgreSQL.

```bash
export PARKRR_DATABASE_URL="postgres://parkrr:parkrr@localhost:5432/parkrr?sslmode=disable"
export PARKRR_ADMIN_PASSWORD="dev-admin-pw"
export PARKRR_SESSION_SECRET="dev-session-secret-please-change"

go mod tidy
go run ./cmd/parkrr        # http://localhost:8080
```

Qualität:

```bash
go test ./...
go vet ./...
golangci-lint run          # Linting
gosec ./...                # Security
govulncheck ./...          # Vulnerabilities
```

---

## 🔁 CI / CD

GitHub-Actions-Workflows unter `.github/workflows/`:

| Workflow | Zweck |
| --- | --- |
| `ci.yml` | Build, `go vet`, Tests (Race-Detector), Docker-Build |
| `golangci-lint.yml` | Statische Analyse / Linting (golangci-lint v2) |
| `gosec.yml` | Security-Scanner |
| `govulncheck.yml` | Bekannte Schwachstellen (auch wöchentlich) |
| `deadcode.yml` | Toter / unerreichbarer Code (schlägt bei Funden fehl) |
| `gitleaks.yml` | Secret-Scanning der Git-Historie (auch wöchentlich) |
| `docker-publish.yml` | Multi-Arch-Image (amd64 + arm64) nach **GHCR** |
| `dependency-review.yml` | Dependency-Review + Modul-Graph |

**Dependabot** hält Go-Module, GitHub-Actions und Docker-Basis-Images aktuell.

### Vorgebautes Image aus GHCR

`docker-publish.yml` veröffentlicht bei jedem Push auf `main` (und für Tags `v*`)
ein Multi-Arch-Image nach `ghcr.io/<owner>/parkrr`. Ohne lokalen Build starten:

```bash
cp .env.example .env   # Pflichtwerte setzen (Admin-PW, Session-Secret …)
docker compose -f docker-compose.ghcr.yml up -d
# feste Version statt latest:  PARKRR_TAG=1.2 docker compose -f docker-compose.ghcr.yml up -d
```

---

## 🤝 Mitmachen

Beiträge sind willkommen! Bitte lies [CONTRIBUTING.md](CONTRIBUTING.md).
Kurz: Fork → Branch → `go test ./... && golangci-lint run` grün → Pull Request.

---

## 📄 Lizenz

[MIT](LICENSE) – frei nutzbar, ohne Lizenzkosten. Es werden ausschließlich freie
Werkzeuge und Bibliotheken verwendet.

> **Hinweis:** Bereitgestellt „wie besehen", ohne Gewähr. Für den produktiven
> Betrieb selbst für TLS, Backups (`pg_dump`) und Updates sorgen.
