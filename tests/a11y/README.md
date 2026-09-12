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
`PARKRR_WEBAUTHN_ORIGINS=http://localhost:18099` — und überspringt sich sonst
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

`compact-workflows.spec.js` adds isolated form-contract checks for progressive
disclosure, catalog autocomplete, live totals, date shortcuts, locked vehicle
prices, preserved bindings on lookup failure, agreement vehicles, tariff coupling,
both portal languages and keyboard/axe/reflow checks at 320/390/1440px. It needs
no backend. Run it alongside the three synthetic suites above. The real-backend
record and portal tests explicitly open the newly disclosed controls.

## Reproduce the before/after gallery

From the repository root, with Chromium and the dependencies above installed:

```sh
node tests/a11y/capture-before-after.cjs
```

This reads the complete static assets from three fixed Git revisions without
checking out another branch: original `9756ae2`, pre-form-refinement `539fb45`,
and current UI `bc5c86e`. Every API response is synthetic. No app, credentials,
database, external service or Docker container is required. The script does not
submit forms. It uses the same fixtures, date, German locale, Europe/Vienna time
zone, 1× pixel ratio and viewport for every revision.

Output: `.impeccable/review/before-after/index.html`, capture PNGs and a provenance
manifest with commit IDs, asset/fixture/image hashes and gallery checks. The viewer
switches between the latest and complete overhaul, 19 pages and 7 form states,
desktop/light and mobile/dark. Pages use full-page captures; dialogs use an equal
900px-tall viewport, including their ordinary scroll areas. Historical overflow
is preserved (the original mobile planner produces a 445px-wide full-page image
at a 390px viewport); actual PNG dimensions are recorded and checked. Generated
output is Git-ignored; the capture script and viewer template are versioned.

The generator checks all comparison selections, image decoding, keyboard focus,
missing-image feedback, 320/390/1440px reflow, direct file opening and axe A/AA.
Use `node tests/a11y/capture-before-after.cjs --verify-only` to rerun those checks
against an existing gallery; it first verifies revisions, fixtures and PNG hashes.
Those checks validate the comparison gallery, not a new run of the app's full
functional suite. To compare a later UI revision, update the explicit revision
mapping in the script and regenerate the captures and viewer together.
