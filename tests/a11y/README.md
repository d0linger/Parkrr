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

The current suite audits the login screen (reachable unauthenticated). Extend
`a11y.spec.js` with authenticated flows as coverage grows.
