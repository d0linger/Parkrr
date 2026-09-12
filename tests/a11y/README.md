# Browser functionality and accessibility tests

Playwright functional checks and axe-core WCAG 2.1 A/AA checks. The separate
`a11y.yml` CI workflow runs this suite against its own temporary app and database.
Node and Chromium are required.

**Use a disposable backend for the full suite.** Existing tests create records,
change account/security settings and exercise backup configuration. Do not point
them at your normal local instance on port 8099 or any production database.

```bash
cd tests/a11y
npm install
npx playwright install --with-deps chromium

# Point at an independently configured disposable instance.
PARKRR_BASE_URL=http://localhost:18099 PARKRR_E2E_ISOLATED=1 npm test -- --workers=1 --retries=0
```

**Wichtig:** Die Ziel-Instanz braucht `PARKRR_RATE_LIMIT_PER_MIN` deutlich erhöht
(z. B. `20000`). Die Suite feuert von EINER IP, und der Standardwert (600/min)
drosselt sie mit HTTP 429 — die Tests fallen dann an wechselnden Stellen um und
sehen aus wie Flakiness. Für die Backup-Prüfungen muss außerdem
`PARKRR_BACKUP_KEY` gesetzt sein, sonst rendert die Backup-Ansicht nur
„Nicht aktiviert". Der Passkey-E2E (auth-flows.spec.js) läuft nur, wenn die
Instanz WebAuthn kann — `PARKRR_WEBAUTHN_RP_ID=localhost` und
`PARKRR_WEBAUTHN_ORIGINS=http://localhost:8099` — und überspringt sich sonst
selbst mit Begründung.

Use dedicated test credentials via `PARKRR_E2E_USER` / `PARKRR_E2E_PASS` (defaults:
`admin` / `ci-a11y-admin-password`). For a disposable app on port 18099, its
WebAuthn origin must also be `http://localhost:18099`. Set breached-password
lookups off only in that test environment to avoid a third-party dependency.

The suite includes real-backend authentication, virtual passkeys, 2FA, uploads,
exports, portal requests, planner interactions and record/payment persistence.
`record-workflow.spec.js` requires the explicit `PARKRR_E2E_ISOLATED=1` opt-in;
that flag is an acknowledgement, not an automatic safety check or database reset.

## Isolated UI refinement checks

`ui-refinement.spec.js` serves local static assets with synthetic API responses.
It does not require a running Parkrr instance or database. With dependencies and
Chromium installed, run only this suite:

```sh
node node_modules/@playwright/test/cli.js test ui-refinement.spec.js --workers=1 --retries=0
```

It covers responsive light/dark previews, keyboard and forced-color behavior,
long content, retry and pending states, and concurrent dashboard loading.

`page-overhaul.spec.js` adds synthetic populated records for all 17 operator
routes, login and the customer portal, plus page-specific interactions and error
recovery. Run both without a live server:

```sh
node node_modules/@playwright/test/cli.js test page-overhaul.spec.js ui-refinement.spec.js ui-functional-regressions.spec.js --workers=1 --retries=0
```

Page-by-page captures are written to `.impeccable/review/page-overhaul/` at the
project root; these generated images are ignored by Git.

`ui-functional-regressions.spec.js` additionally covers invoice-paid standalone
charges, expired sessions, exact form payloads, duplicate submissions, failed
saves and retries, QR image decode failures, role restrictions and late portal
responses. These checks use synthetic API responses, not live backend records.
Calendar-sensitive synthetic tests pin the date to 2026-09-10.

Frontend unit tests can be run from the project root without a browser:

```sh
node --test tests/geometry/geometry.test.js tests/frontend/*.test.js
```
