// @ts-check
// Hundert 44: ein CSP-Verstoss (z. B. ein versehentliches Inline-Style) landet
// ueber die client-error-Telemetrie im Server-Log statt nur in der Konsole des
// betroffenen Nutzers.
const { test, expect } = require('@playwright/test');
test.setTimeout(60000);

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

test('CSP-Verstoss wird an client-error gemeldet', async ({ page }) => {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  const reports = [];
  page.on('request', (req) => { if (req.url().includes('/api/client-error')) reports.push(JSON.parse(req.postData() || '{}')); });
  const acceptedReport = page.waitForResponse(response => new URL(response.url()).pathname === '/api/client-error' && response.request().method() === 'POST');

  // Einen Verstoss erzwingen: ein style-Attribut verletzt style-src 'self'.
  await page.evaluate(() => {
    const d = document.createElement('div');
    d.setAttribute('style', 'color:red');
    document.body.append(d);
  });
  await page.waitForTimeout(1200);
  const response = await acceptedReport;
  expect(response.status()).toBe(204);
  expect(response.request().headers()['content-type']).toBe('application/json');

  expect(reports.length, 'kein client-error-Report angekommen').toBeGreaterThan(0);
  expect(reports.some((r) => String(r.message || '').startsWith('CSP:'))).toBeTruthy();
  for (const report of reports) expect(Object.keys(report).sort()).toEqual(['message', 'stack', 'url', 'version']);
});
