// @ts-check
// Der CSV-Export der Belege (Hundert 19): die beiden neuen Ziele muessen in der
// Oberflaeche verlinkt sein UND echte Daten liefern — ein Link, der 404 liefert,
// faellt sonst erst dem Steuerberater auf.
const { test, expect } = require('@playwright/test');

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

test('Export: Rechnungen und Zusatzkosten sind verlinkt und liefern CSV', async ({ page }) => {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  await page.evaluate(() => { location.hash = '#/finance'; });
  await page.waitForSelector('a[href="/api/export/invoices"]', { timeout: 15000 });
  await expect(page.locator('a[href="/api/export/charges"]')).toHaveCount(1);

  for (const entity of ['invoices', 'charges']) {
    const res = await page.evaluate(async (e) => {
      const r = await fetch('/api/export/' + e, { credentials: 'same-origin' });
      return { status: r.status, ct: r.headers.get('content-type'), head: (await r.text()).slice(0, 120) };
    }, entity);
    expect(res.status, entity).toBe(200);
    expect(res.ct, entity).toContain('text/csv');
    // Semikolon-getrennte Kopfzeile mit BOM davor.
    expect(res.head, entity).toContain(';');
  }
});
