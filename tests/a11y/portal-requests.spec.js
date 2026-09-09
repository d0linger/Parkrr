// @ts-check
// Hundert 85/87: der Portal-Briefkasten. Kunde reicht neue Kontaktdaten und einen
// Abholtermin ein; der Betreiber sieht beides auf der Uebersicht und uebernimmt —
// erst DANN aendern sich die Stammdaten.
const { test, expect } = require('@playwright/test');
test.setTimeout(90000);

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

test('Portal-Briefkasten: einreichen, übernehmen, Stammdaten geändert', async ({ page }) => {
  await login(page);
  const seed = await page.evaluate(async () => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const hdr = { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf };
    const person = await (await fetch('/api/persons', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({ first_name: 'Brief', last_name: 'KastenSeed' }) })).json();
    const res = await (await fetch('/api/persons/' + person.id + '/portal-link', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({}) })).json();
    return { pid: person.id, link: res.link };
  });
  const token = seed.link.slice(seed.link.indexOf('/#/portal/') + '/#/portal/'.length);

  // Kunde: Portal oeffnen, Kontaktdaten-Wunsch einreichen.
  await page.goto('about:blank');
  await page.goto('/#/portal/' + token);
  await page.waitForSelector('#portal-view:not([hidden])', { timeout: 15000 });
  const emailIn = page.locator('.portal-form input[type=email]');
  await expect(emailIn).toBeVisible({ timeout: 15000 });
  await emailIn.fill('briefkasten@example.com');
  await page.locator('.portal-form').first().locator('button').click();
  await expect(page.locator('#portal-view')).toContainText(/Submitted|Übermittelt/, { timeout: 15000 });

  // Betreiber: Uebersicht zeigt den Wunsch, Uebernehmen wirkt. Die Admin-Sitzung
  // lebt noch (Cookies im selben Kontext) — direkt zur App, kein zweiter Login.
  await page.goto('about:blank');
  await page.goto('/');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
  await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
  const inbox = page.locator('.card', { hasText: 'Kundenwünsche' });
  await expect(inbox).toBeVisible({ timeout: 15000 });
  await expect(inbox).toContainText('briefkasten@example.com');
  await inbox.getByRole('button', { name: 'Übernehmen' }).first().click();
  await page.waitForTimeout(800);

  const person = await page.evaluate(async (pid) => {
    const list = await (await fetch('/api/persons', { credentials: 'same-origin' })).json();
    return list.find((p) => p.id === pid);
  }, seed.pid);
  expect(person.email).toBe('briefkasten@example.com');

  // Aufraeumen.
  await page.evaluate(async (pid) => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    await fetch('/api/persons/' + pid, { method: 'DELETE', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf } });
  }, seed.pid);
});
