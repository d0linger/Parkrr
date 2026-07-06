// @ts-check
const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;

// Smoke-level accessibility checks against the login screen (the only view
// reachable without authentication). Fails on WCAG 2.1 A/AA violations.
test('login screen has no critical accessibility violations', async ({ page }) => {
  await page.goto('/');
  // The SPA reveals the login card once bootstrapped.
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 10000 });

  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa'])
    .analyze();

  const serious = results.violations.filter(
    (v) => v.impact === 'serious' || v.impact === 'critical',
  );
  if (serious.length) {
    console.log(JSON.stringify(serious, null, 2));
  }
  expect(serious).toEqual([]);
});
