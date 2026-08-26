<div align="center">

<img src="web/static/icons/icon.svg" width="104" alt="Parkrr logo">

# Parkrr

**Self-hosted management for vehicle storage spaces** — vehicles, people and costs,
as a mobile-first PWA. Written in Go, backed by PostgreSQL, shipped entirely via Docker.

[![CI](https://github.com/d0linger/Parkrr/actions/workflows/ci.yml/badge.svg)](https://github.com/d0linger/Parkrr/actions/workflows/ci.yml)
[![golangci-lint](https://github.com/d0linger/Parkrr/actions/workflows/golangci-lint.yml/badge.svg)](https://github.com/d0linger/Parkrr/actions/workflows/golangci-lint.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-14b8a6.svg)](LICENSE)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![PWA](https://img.shields.io/badge/PWA-installable-0d9488)
![Self-hosted](https://img.shields.io/badge/self--hosted-Docker-2496ED?logo=docker&logoColor=white)

</div>

Parkrr helps small operators (storage-space rental, winter storage, camping and
boat storage …) manage **vehicles** (cars, trailers, caravans, motorhomes,
motorcycles …) that **people** put into storage, and compute the **ongoing costs**
per vehicle — monthly or yearly, prorated to the day.

> All frontend assets (CSS, JS, icons, charts) are served **locally** — no external
> CDNs, no tracking, no cloud. Your data stays with you.
>
> The application UI is in **German**; this README is in English.

---

## 📸 Screenshots

> The screenshots follow your theme automatically — **light on GitHub's light theme, dark on its dark theme**.

<table>
<tr>
<td align="center"><b>Login</b></td>
<td align="center"><b>Dashboard</b></td>
<td align="center"><b>Gefährte</b></td>
<td align="center"><b>Person</b></td>
</tr>
<tr>
<td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/01-login-dark.png"><img alt="Login screen" src="docs/screenshots/01-login.png" width="200"></picture></td>
<td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/02-dashboard-dark.png"><img alt="Dashboard overview" src="docs/screenshots/02-dashboard.png" width="200"></picture></td>
<td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/03-vehicles-dark.png"><img alt="Vehicle list with status sliders" src="docs/screenshots/03-vehicles.png" width="200"></picture></td>
<td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/04-person-dark.png"><img alt="Person detail with balance" src="docs/screenshots/04-person.png" width="200"></picture></td>
</tr>
</table>

<p align="center"><b>Garagenplaner (Stellplatzverwaltung)</b> — a to-scale floor plan per hall, vehicles placed on their spots</p>
<p align="center"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/06-planner-dark.png"><img alt="Garagenplaner: to-scale hall floor plan with vehicles placed on their spots" src="docs/screenshots/06-planner.png" width="760"></picture></p>

---

## ✨ Features

- **People & vehicles** — multiple vehicles per person, a lean form, and detail
  pages (deep-linkable) with balance, charts and history.
- **Slider controls** — set the storage and payment status per vehicle straight
  from a slider (*reserved · stored · collected* and *open · paid*), one tap, no form.
- **Seasonal re-use** — *"↻ Store again"* duplicates a collected vehicle (type,
  plate, price **and photos**) with a new start date.
- **Lifecycle & reservations** — status history (who/when/note); the end date is
  set automatically on collection.
- **Garagenplaner (Stellplatzverwaltung)** — a to-scale floor-plan editor per
  hall: draw walls, mark exclusion zones and place/rotate each vehicle on its
  spot, with live fit checks (footprint inside the hall, height ≤ door, weight ≤
  floor load) and a colour-coded occupancy bar. Import a **DXF or image** as a
  plan underlay. Structured as garages → halls → spots.
- **Photos** — per vehicle (JPEG/PNG), validated and re-encoded on upload
  (EXIF/GPS stripped), gallery with lightbox.
- **Handover protocols (Übergabeprotokoll)** — record the condition and a **drawn
  signature** on check-in and on collection; download each protocol as a PDF.
- **Central tariffs** — vehicle types with default prices, **overridable per
  vehicle** (special price); monthly/yearly price optionally **coupled**
  (year = month × 12, one field fills the other automatically).
- **Per-person flat rate (Pauschale)** — when someone stores several vehicles and
  agrees **one flat amount** (monthly or yearly) covering all of them instead of
  per-vehicle billing: prorated to the day, with **paid status per year**
  (open/paid); the vehicles then show as "covered by the flat rate".
- **Cost tracking** — day-accurate costs from the start date to the collection
  date (or to today).
- **Extra & recurring charges** — one-off items (cleaning, winter service …) from
  a service catalog, plus **recurring** charges (e.g. monthly electricity) that
  bill automatically per completed period.
- **Invoicing (Rechnungen)** — issue period invoices (Austrian **BAO**-compliant,
  immutable once issued — corrections are reversals, not silent edits), download
  as **PDF**, attach a **payment QR code**, send **reminders**, and track overdue.
- **Payments & credit (Guthaben)** — record a payment, auto-allocate it across the
  open items, and carry any remainder forward as credit; reversing a payment
  re-opens exactly what it had covered.
- **Statistics & charts** — revenue/month, status distribution, paid/open, and
  cost per person by month and year (local SVG charts).
- **CSV import & export** — bulk-import people from a CSV (with a downloadable
  example template), and export lists (people, vehicles, invoices, occupancy …) as CSV.
- **Fuzzy search** — one server-side command palette across people, vehicles,
  invoices, garages, halls, tariffs and services: German stemming,
  diacritics-folded (finds *Müller* for `muller`) and typo-tolerant.
- **Self-service portal** — a per-person magic link lets a customer see their
  balance, download invoice PDFs and open a payment QR code — no login, no
  account.
- **Backups** — scheduled, **encrypted** (AES-256-GCM) database dumps to a local
  volume and optionally **off-site to S3** (any S3-compatible store), with a
  restore path and a health check.
- **Multi-user & roles** — *Admin, Editor, Reader* (see [Roles & permissions](#-roles--permissions)).
- **Security** — passkeys (WebAuthn), 2FA (TOTP) + **recovery codes**, hashed
  session tokens, rate limiting, hardened HTTP headers. See [Security](#-security).
- **PWA** — installable on a phone, light/dark, offline-capable app shell.

---

## 🚀 Quick start (Docker)

Requirements: Docker + Docker Compose.

```bash
git clone https://github.com/d0linger/Parkrr.git
cd Parkrr

# 1. Configuration
cp .env.example .env
#   set at least, in .env:
#   - PARKRR_ADMIN_PASSWORD
#   - PARKRR_SESSION_SECRET   (e.g.:  openssl rand -base64 48)
#   - PARKRR_DB_PASSWORD

# 2. Start
docker compose up -d --build

# 3. Open:  http://localhost:8080
#    Log in with PARKRR_ADMIN_USERNAME / PARKRR_ADMIN_PASSWORD
```

The database runs as a standalone Postgres container (reachable only on the
compose network). Schema migrations run automatically at startup.

---

## ⚙️ Configuration

| Variable | Description | Default |
| --- | --- | --- |
| `PARKRR_LISTEN_ADDR` | HTTP listen address | `:8080` |
| `PARKRR_HTTP_PORT` | published host port (compose) | `8080` |
| `PARKRR_DATABASE_URL` | full Postgres URL (overrides the discrete values) | – |
| `PARKRR_DB_HOST` / `PARKRR_DB_PORT` | DB host / port | `db` / `5432` |
| `PARKRR_DB_USER` / `PARKRR_DB_PASSWORD` / `PARKRR_DB_NAME` | DB credentials | `parkrr` |
| `PARKRR_DB_SSLMODE` | Postgres SSL mode | `disable` |
| `PARKRR_ADMIN_USERNAME` | admin username | `admin` |
| `PARKRR_ADMIN_EMAIL` | admin email | `admin@example.com` |
| `PARKRR_ADMIN_PASSWORD` | **required** — admin password | – |
| `PARKRR_SESSION_SECRET` | **required** — session secret (min. 32 chars) | – |
| `PARKRR_SESSION_MAX_AGE` | session lifetime / inactivity window (sec.) | `604800` (7 days) |
| `PARKRR_SESSION_SLIDING` | extend the session on activity (re-login only after inactivity) | `false` |
| `PARKRR_SESSION_ABSOLUTE_MAX_AGE` | hard cap on session lifetime (sec., when sliding) | `7776000` (90 days) |
| `PARKRR_WEBAUTHN_RP_ID` | enable passkeys: registrable domain (no scheme/port) | – (off) |
| `PARKRR_WEBAUTHN_RP_NAME` | display name shown by the authenticator | `Parkrr` |
| `PARKRR_WEBAUTHN_ORIGINS` | allowed origins, e.g. `https://parkrr.example.com` | – |
| `PARKRR_SECURE_COOKIES` | `Secure` flag on cookies (secure-by-default; `false` only for plain-HTTP dev on non-`localhost`) | `true` |
| `PARKRR_TRUSTED_PROXY` | behind a reverse proxy: trust `X-Forwarded-*` | `false` |
| `PARKRR_TRUSTED_PROXY_CIDRS` | **required when `PARKRR_TRUSTED_PROXY=true`** — comma-separated proxy IP range(s); `X-Forwarded-*` is honoured only from these peers (startup fails if trusted-proxy is on and this is empty) | – |
| `PARKRR_PUBLIC_BASE_URL` | external base URL (self-service portal links, email) | – |
| `PARKRR_RATE_LIMIT_PER_MIN` | general per-IP request budget/minute (`0` = off) | `600` |
| `PARKRR_AUDIT_RETENTION_DAYS` | long window: prune audit entries older than N days (`0` = disables **this** window only, see below). Records of account (`invoice`, `payment`, `billing`, `flatrate`, `recurring_charge`) are never pruned at any age | `2555` (7 y, BAO §132) |
| `PARKRR_AUDIT_RETENTION_SHORT_DAYS` | short window for auth/ops noise only (`login`, `logout`, `backup`, `remind`, `import`); `0` disables the short tier | `365` |
| `PARKRR_METRICS_TOKEN` | Bearer token for `/metrics` (empty = open on an internal network) | – |
| `PARKRR_METRICS_REQUIRE_AUTH` | refuse to serve `/metrics` unless a token is set | `false` |
| `PARKRR_CHECK_BREACHED_PASSWORDS` | check new passwords against the HIBP range API (fail-open) | `true` |
| `PARKRR_LOG_FORMAT` / `PARKRR_LOG_LEVEL` | `json`\|`text` / `debug`..`error` | `json` / `info` |

> The admin account is created/updated from the ENV values on **every start** —
> the ENV remains the source of truth for the admin.

**Backups & email** (all optional — a feature stays off until its key variable is set):

| Variable | Description | Default |
| --- | --- | --- |
| `PARKRR_BACKUP_KEY` | passphrase enabling scheduled **encrypted** DB backups (AES-256-GCM); empty = backups off | – |
| `PARKRR_BACKUP_DIR` | directory for local dumps (a mounted volume) | `/backups` (compose) |
| `PARKRR_S3_ENDPOINT` / `PARKRR_S3_REGION` | off-site target: S3-compatible endpoint / region | – |
| `PARKRR_S3_BUCKET` / `PARKRR_S3_PREFIX` | bucket and key prefix for uploaded backups | – |
| `PARKRR_S3_ACCESS_KEY` / `PARKRR_S3_SECRET_KEY` | S3 credentials | – |
| `PARKRR_S3_USE_SSL` | use TLS to the S3 endpoint | `true` |
| `PARKRR_SMTP_HOST` / `PARKRR_SMTP_PORT` | SMTP server for payment reminders & portal links; empty = email off | – / `587` |
| `PARKRR_SMTP_USERNAME` / `PARKRR_SMTP_PASSWORD` | SMTP credentials | – |
| `PARKRR_SMTP_FROM` / `PARKRR_SMTP_FROM_NAME` | envelope/display sender | – |
| `PARKRR_SMTP_TLS` | `starttls` \| `tls` \| `none` | `starttls` |

---

## 🔐 Passkeys (WebAuthn)

Passwordless, phishing-resistant sign-in via fingerprint/face recognition. Enabled
as soon as `PARKRR_WEBAUTHN_RP_ID` is set (requires HTTPS, e.g. via your proxy —
`http://localhost` also works, as browsers treat it as a secure context):

```env
PARKRR_WEBAUTHN_RP_ID=parkrr.example.com
PARKRR_WEBAUTHN_ORIGINS=https://parkrr.example.com
```

<div align="center">
<picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/05-settings-dark.png"><img src="docs/screenshots/05-settings.png" width="300" alt="Settings with the Passkeys card"></picture>
</div>

- **Enroll:** **Settings → Passkeys → "+ Passkey hinzufügen"** (after a normal login).
- **Sign in:** the **"Mit Passkey anmelden"** button on the login screen —
  usernameless (the passkey carries your account handle).
- Password + TOTP + recovery codes remain as the fallback / recovery path.
- The server stores only **public keys** — no secret material; the private key
  never leaves the device. A passkey is itself MFA, so TOTP is **not** additionally
  requested on a passkey login.

> **Where do I configure passkeys?** It's a server-side switch: passkeys are off
> until `PARKRR_WEBAUTHN_RP_ID` is set. Once it is, the **Passkeys** card appears
> in Settings and the passkey button appears on the login screen. `RP_ID` and
> `ORIGINS` must match the exact URL you open Parkrr at.

---

## 📈 Health, readiness & metrics

| Endpoint | Purpose | Auth |
|----------|---------|------|
| `GET /healthz` | liveness (process up, version) | open |
| `GET /readyz` | readiness (DB reachable) — `503` if not | open |
| `GET /metrics` | Prometheus (request count/latency, DB pool) | `PARKRR_METRICS_TOKEN` (Bearer), if set |

The container ships a `HEALTHCHECK` (`parkrr healthcheck`, self-probing — the
image ships no curl/wget). The **audit log is append-only** (a DB
trigger blocks UPDATE/DELETE); only the retention job
(`PARKRR_AUDIT_RETENTION_DAYS` / `PARKRR_AUDIT_RETENTION_SHORT_DAYS`) may remove
old entries. The two windows are independent: setting the long one to `0` keeps
the change trail forever but does **not** stop the short tier from ageing out
login/logout noise — set both to `0` to disable retention completely.

---

## 🌐 Running behind a reverse proxy (Nginx Proxy Manager, Traefik, Caddy …)

Parkrr uses only **relative paths** and listens on `:8080` — it runs behind a
reverse proxy on its own (sub)domain without changes. TLS is terminated at the proxy.

**1. Adjust `.env`:**

```env
PARKRR_TRUSTED_PROXY=true                       # trust X-Forwarded-For/-Proto (behind a proxy only!)
PARKRR_TRUSTED_PROXY_CIDRS=172.16.0.0/12        # required when the above is true: the proxy's IP range(s)
PARKRR_SECURE_COOKIES=true                      # default; correct behind TLS/proxy (use false only for plain-HTTP dev)
```

With `PARKRR_TRUSTED_PROXY=true`, Parkrr uses the real client IP from
`X-Forwarded-For`/`X-Real-IP` (for logs, rate limiting, audit) and detects HTTPS
via `X-Forwarded-Proto` (→ `Secure` cookies + HSTS). `X-Forwarded-*` headers are
honoured **only** from a peer inside `PARKRR_TRUSTED_PROXY_CIDRS`, so this
allowlist is **required** whenever the trusted-proxy switch is on — the app
**refuses to start** without it (otherwise any direct client could spoof its own
IP). Set it to your proxy's address range (e.g. the Docker bridge subnet). **Without**
a proxy, keep `PARKRR_TRUSTED_PROXY=false`.

**2. Nginx Proxy Manager — Proxy Host:**

- **Scheme** `http` · **Forward Hostname** `parkrr-app` (on a shared Docker network)
  or the host IP · **Forward Port** `8080` · **Websockets** not needed
- **SSL** tab: Let's Encrypt certificate, enable *Force SSL* + *HTTP/2*.
  NPM sets `X-Forwarded-Proto`/`X-Forwarded-For` automatically.

For direct access on the Docker network, put both containers on the same network
and remove the `ports:` block of the `app` service in `docker-compose.yml`.

**3. Generic Nginx example:**

```nginx
server {
    listen 443 ssl http2;
    server_name parkrr.example.com;
    # ssl_certificate ...;  ssl_certificate_key ...;

    client_max_body_size 10m;   # photo uploads (up to 8 MB)

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

## 🛡️ Rootless & hardened deployment

Parkrr runs cleanly under a **rootless engine** (rootless Docker or Podman). It
needs no privileged features, no host networking, and no bind mounts: the app
image already runs as a **non-root** user (the `parkrr` user, uid 10001),
photos live in the database, and the DB uses a **named volume**. The published
port (`${PARKRR_HTTP_PORT:-8080}:8080`) is ≥ 1024, so no extra capability is
required to bind it.

**Rootless Docker (Ubuntu):**

```bash
sudo apt install -y uidmap dbus-user-session
dockerd-rootless-setuptool.sh install      # run as your non-root user
loginctl enable-linger "$USER"             # keep containers up after logout
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/docker.sock
docker compose up -d                        # compose file is unchanged
```

**Podman (rootless by default):** `podman compose up -d` — no daemon, no setup tool.

**Portainer:** point the endpoint at the rootless socket
(`/run/user/<uid>/docker.sock`) instead of `/var/run/docker.sock`, then deploy
Parkrr as a **Stack** (set the env vars in the Stack UI instead of `.env`).

> **Migrating existing data:** a rootless engine uses a different data root
> (`~/.local/share/docker`), so a previous rootful `parkrr-db-data` volume is not
> visible. Migrate with `pg_dump` from the old stack and restore into the new one
> (don't copy the volume directory — the UID mapping differs).

### Extra hardening overlay

`docker-compose.hardened.yml` adds `no-new-privileges`, capability dropping, a
read-only app rootfs and resource limits. Apply it on top of either base file
(works rootful **and** rootless):

```bash
docker compose -f docker-compose.yml      -f docker-compose.hardened.yml up -d
docker compose -f docker-compose.ghcr.yml -f docker-compose.hardened.yml up -d
```

The app is locked down hard (read-only rootfs, `cap_drop: ALL`); Postgres is
hardened more conservatively (it keeps the few capabilities its entrypoint needs).

---

## 👥 Roles & permissions

| Role | Read | People, vehicles, charges, tariffs, services | Users & audit log |
| --- | :---: | :---: | :---: |
| **Admin** | ✓ | ✓ | ✓ |
| **Editor** (`editor`) | ✓ | ✓ | – |
| **Reader** (`reader`) | ✓ | – | – |

Editors can do everything **except** user management and viewing the audit log.
New users get the *Editor* role by default. The last remaining admin cannot be
demoted or deleted.

---

## 🔒 Security

- **Passwords** with bcrypt; **session tokens hashed** (SHA-256) in the DB.
- **Passkeys (WebAuthn)** — phishing-resistant, passwordless; the server stores
  only public keys (no secret material).
- **CSRF** via double-submit token; **role-based** authorization.
- **2FA (TOTP)** with QR enrollment and single-use **recovery codes** (regenerable
  any time); the TOTP secret is stored **encrypted** (AES-GCM). **Admins** can
  **reset** a user's 2FA (on device loss).
- **Session hardening** — a password change rotates the session and signs out
  other devices; new passwords are optionally checked against **HIBP**
  (`PARKRR_CHECK_BREACHED_PASSWORDS`, fail-open); login and 2FA verification are
  rate-limited. Optional **sliding sessions** re-login only after inactivity.
- **Rate limiting** — login lockout after too many failures **plus** a general
  per-IP throttle.
- **Photo uploads** are decoded and re-encoded (**EXIF/GPS stripped**), accept
  only genuine JPEG/PNG, with dimension and count limits.
- Hardened HTTP headers: **CSP**, **HSTS** (behind TLS), `Permissions-Policy`,
  COOP/CORP; session cookies `HttpOnly` + `SameSite`.
- **Session management** — view active devices, sign out individually or "everywhere".
- **Privacy (DSGVO/GDPR)** — **anonymise** a person who can't be deleted (their
  invoices must stay for tax law): the master record is cleared and their portal
  links revoked, while the receipts remain intact.
- **Audit log** of every change; structured logs (slog, JSON/text) with a
  request ID **and the signed-in user** per access. Login/logout and failed
  attempts are logged with user, IP and reason.
- Runs as a **non-root** user (`parkrr`, uid 10001) in a minimal Alpine-based container.

Please do **not** report vulnerabilities via public issues — see
[SECURITY.md](SECURITY.md).

---

## 🧮 Cost calculation

Effective price = special price, otherwise the central tariff price for the chosen
billing mode. Accrued costs are prorated **to the calendar** from the start date
to the collection date (or to today) — against the **actual** length of each
calendar month/year:

- for any month/year that lies fully within the range: **exactly** the monthly/yearly price
- for partial months/years: `price × days in range / days in the calendar month or year`

So a full calendar year costs exactly the yearly price (500 € rather than the
499.66 € an average year length of 365.25 days would give), and only genuinely
partial periods are charged pro rata. Internally the math runs in **integer
cents**, so summing many periods never drifts.

For **tariffs**, the monthly and yearly price can optionally be **coupled**
(year = month × 12): editing one field then fills the other automatically;
without coupling, both prices are set freely.

"Paid" is derived from the **payment slider** per vehicle (or **per year** for a
flat rate); open balance = accrued rent + extra charges − paid.

---

## 🏗️ Architecture

```text
cmd/parkrr/        – entry point, admin bootstrap, logging, server lifecycle
internal/config/   – configuration from ENV
internal/database/ – pgx pool + embedded SQL migrations (advisory-locked)
internal/models/   – domain types + cost calculation (with tests)
internal/auth/     – bcrypt, sessions/CSRF, roles, 2FA (TOTP/AES-GCM),
                     passkeys (WebAuthn), rate limiting, HIBP
internal/backup/   – scheduled encrypted DB dumps, S3 off-site, verify/restore
internal/handlers/ – JSON API (auth/2FA/passkeys, people, vehicles, photos,
                     tariffs, services, charges, stats, users, audit, search,
                     self-service portal, backups)
internal/server/   – routing, middleware (access log, rate limit, security
                     headers, metrics), health/readiness/metrics endpoints
web/static/        – PWA frontend (SPA, SVG charts, service worker, icons)
```

- **Backend:** Go standard library (`net/http`, method-based routing); runtime
  dependencies are `pgx` (PostgreSQL), `golang.org/x/crypto` + `golang.org/x/sync`,
  `pquerna/otp` (TOTP), `go-webauthn` (passkeys), `go-pdf/fpdf` (invoice PDFs),
  `boombuler/barcode` (payment QR codes), `minio-go` (S3 backups),
  `robfig/cron` (scheduled backups) and the Prometheus client.
- **Frontend:** vanilla-JS SPA with hash routing, native `<dialog>` modals, SVG
  charts, modern CSS — no framework, no build step.

---

## 🧑‍💻 Development (without Docker)

Requirements: Go 1.26+, a running PostgreSQL.

```bash
export PARKRR_DATABASE_URL="postgres://parkrr:parkrr@localhost:5432/parkrr?sslmode=disable"
export PARKRR_ADMIN_PASSWORD="dev-admin-pw"
export PARKRR_SESSION_SECRET="dev-session-secret-please-change"

go mod tidy
go run ./cmd/parkrr        # http://localhost:8080
```

Quality gates:

```bash
go test ./...
go vet ./...
golangci-lint run          # linting
gosec ./...                # security
govulncheck ./...          # vulnerabilities
```

Database-backed integration tests run when `PARKRR_TEST_DATABASE_URL` is set
(they skip otherwise). Optional accessibility checks live in `tests/a11y/`.

---

## 🔁 CI / CD

GitHub Actions workflows under `.github/workflows/`:

| Workflow | Purpose |
| --- | --- |
| `ci.yml` | build, `go vet`, tests (race detector) + coverage gate, Docker build |
| `a11y.yml` | accessibility smoke tests (Playwright + axe-core) |
| `golangci-lint.yml` | static analysis / linting (golangci-lint v2) |
| `gosec.yml` | security scanner |
| `govulncheck.yml` | known vulnerabilities (also weekly) |
| `deadcode.yml` | dead / unreachable code (fails on findings) |
| `gitleaks.yml` | secret scanning of the git history (also weekly) |
| `docker-publish.yml` | multi-arch image (amd64 + arm64) to **GHCR** |
| `dependency-review.yml` | dependency review + module graph |

**Dependabot** keeps Go modules, GitHub Actions and Docker base images up to date.

### Prebuilt image from GHCR

`docker-publish.yml` publishes a multi-arch image to `ghcr.io/<owner>/parkrr` on
every push to `main` (and for `v*` tags). Start without a local build:

```bash
cp .env.example .env   # set the required values (admin password, session secret …)
docker compose -f docker-compose.ghcr.yml up -d
# pin a version instead of latest:  PARKRR_TAG=1.2 docker compose -f docker-compose.ghcr.yml up -d
```

### Deploy with Portainer (paste your .env)

Portainer's stack env vars are used for `${VAR}` **substitution** in the compose
(exactly like a `.env` file). Parkrr's `environment:` block forwards **every**
`PARKRR_*` variable, so pasting your `.env` populates the container.

1. **Stacks → Add stack →** *Web editor*.
2. Paste the contents of **`docker-compose.ghcr.yml`** (image-based — Portainer
   pulls `ghcr.io/d0linger/parkrr`, no build needed).
3. Below the editor → **Environment variables → Advanced mode** → paste your
   whole **`.env`** (based on `.env.example`; every line is a clean `KEY=value`).
   At minimum set `PARKRR_ADMIN_PASSWORD`, `PARKRR_SESSION_SECRET` (≥32 chars)
   and `PARKRR_DB_PASSWORD` — deployment fails fast if the first two are missing.
4. **Deploy the stack**, then open `http://<host>:${PARKRR_HTTP_PORT}` (default 8080).

Notes:
- **Passkeys** need HTTPS + a matching domain — set `PARKRR_WEBAUTHN_RP_ID` and
  `PARKRR_WEBAUTHN_ORIGINS` to the exact URL you reach Parkrr at (via your proxy).
- Behind a reverse proxy, set `PARKRR_TRUSTED_PROXY=true`.
- Portainer's single-file editor can't apply the `-f` hardening overlay; to
  harden a Portainer stack, paste the `app`/`db` keys from
  `docker-compose.hardened.yml` into the corresponding services in the editor.

---

## 🤝 Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md).
In short: fork → branch → `go test ./... && golangci-lint run` green → pull request.

---

## 📄 License

[MIT](LICENSE) — free to use, no license fees. Only free tools and libraries are used.

> **Note:** Provided "as is", without warranty. For production, take care of TLS,
> backups (`pg_dump`) and updates yourself.
