// @ts-check
// Accessibility checks on AUTHENTICATED views (AR2). Extends the login-only axe
// smoke test to the main app routes so regressions in the app shell are caught.
const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

async function login(page) {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
}

const views = [
  ['Übersicht', 'dashboard'],
  ['Personen', 'persons'],
  ['Gefährte', 'vehicles'],
  ['Zusatzkosten', 'finance'],
  ['Tarife', 'tariffs'],
];

for (const [label, route] of views) {
  test(`a11y (auth): ${label} has no serious/critical violations`, async ({ page }) => {
    await login(page);
    await page.goto('/#/' + route);
    await page.waitForSelector('#page:not(:empty)', { timeout: 15000 });
    await page.waitForTimeout(400); // settle async list renders

    // Enforces every serious/critical WCAG 2 A/AA rule with no exclusions — the earlier
    // baseline (color-contrast, select-name, aria-prohibited-attr) has been remediated:
    // light-theme AA text inks, aria-label on the filter/sort <select>s, and role="img"
    // on the icon-only "Nicht platziert" span.
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa'])
      .analyze();
    const serious = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
    if (serious.length) {
      console.log(`\n[${label}] ` + serious.map((v) => `${v.id} (${v.nodes.length})`).join(', '));
      console.log(JSON.stringify(serious.map((v) => ({ id: v.id, help: v.help, nodes: v.nodes.map((n) => n.target) })), null, 2));
    }
    expect(serious, `${label}: serious/critical a11y violations`).toEqual([]);
  });
}
