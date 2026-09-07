// @ts-check
// Hundert 89: das Kundenportal kann Englisch. Der Test erstellt einen echten
// Portal-Link, oeffnet ihn, prueft Deutsch als Default (de-Browser), schaltet um
// und prueft, dass die Wahl einen Reload ueberlebt.
const { test, expect } = require('@playwright/test');
test.setTimeout(60000);

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

test('Portal: Sprachumschalter DE/EN mit Gedaechtnis', async ({ page }) => {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  const link = await page.evaluate(async () => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const hdr = { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf };
    const person = await (await fetch('/api/persons', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({ first_name: 'Portal', last_name: 'LangSeed' }) })).json();
    const res = await (await fetch('/api/persons/' + person.id + '/portal-link', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({}) })).json();
    window.__langSeed = person.id;
    return res.link;
  });
  expect(link, 'kein Portal-Link').toContain('/#/portal/');

  const token = link.slice(link.indexOf('/#/portal/') + '/#/portal/'.length);
  // Erst about:blank: ein Hash-Wechsel im selben Dokument laesst init() nicht neu
  // laufen, und nur init() rendert das Portal.
  await page.goto('about:blank');
  await page.goto('/#/portal/' + token);
  await page.waitForSelector('#portal-view:not([hidden])', { timeout: 15000 });
  // Playwright faehrt en-US: der Default folgt der Browsersprache — Englisch.
  // (Ein deutscher Browser bekaeme Deutsch; genau das ist das Feature.)
  await expect(page.locator('#portal-view')).toContainText('Open balance', { timeout: 15000 });
  await expect(page.locator('#portal-view')).toContainText('Your vehicles');

  // Umschalten auf Deutsch.
  await page.locator('.portal-lang').click();
  await expect(page.locator('#portal-view')).toContainText('Offener Betrag', { timeout: 15000 });
  await expect(page.locator('#portal-view')).toContainText('Ihre Gefährte');

  // Die Wahl ueberlebt einen Reload.
  await page.reload();
  await page.waitForSelector('#portal-view:not([hidden])', { timeout: 15000 });
  await expect(page.locator('#portal-view')).toContainText('Offener Betrag', { timeout: 15000 });

  // Aufraeumen als Admin (neue Seite, das Portal hat keine Sitzung).
  await page.goto('/');
  await page.evaluate(async () => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const list = await (await fetch('/api/persons', { credentials: 'same-origin' })).json();
    for (const p of list.filter((x) => x.last_name === 'LangSeed')) {
      await fetch('/api/persons/' + p.id, { method: 'DELETE', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf } });
    }
  });
});
