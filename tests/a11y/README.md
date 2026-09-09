# Accessibility smoke tests

Playwright + axe-core checks for WCAG 2.1 A/AA violations. These are optional
(not wired into the Go CI) and require a running Parkrr instance and Node.

```bash
cd tests/a11y
npm install
npx playwright install --with-deps chromium

# Point at a running instance (defaults to http://localhost:8080)
PARKRR_BASE_URL=http://localhost:8099 npm test
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

The current suite audits the login screen (reachable unauthenticated). Extend
`a11y.spec.js` with authenticated flows as coverage grows.
