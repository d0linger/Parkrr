// @ts-check
// Ein Routenwechsel WAEHREND eines laufenden Seitenaufbaus durfte die neue Seite
// nicht mehr zerstoeren: die alte Route wachte aus ihrem await auf und schrieb ihren
// abgebrochenen fetch als "Fehler: …" in dasselbe #page. Genau das war die Ursache
// der wechselnd fehlschlagenden Oberflaechentests — und im Betrieb der Grund, warum
// schnelles Klicken zwischen zwei Reitern auf einer Fehlerseite landen konnte.
const { test, expect } = require('@playwright/test');
test.setTimeout(60000);

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

test('Schneller Routenwechsel hinterlässt keine Fehlerseite', async ({ page }) => {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  // Die Gefaehrteliste kuenstlich verzoegern, damit ihr Aufbau garantiert noch
  // laeuft, wenn wir weiterspringen — und dann mitten hinein die naechste Route.
  // (Nicht die Uebersicht: nach dem Login steht der Hash schon auf #/dashboard, ein
  // erneutes Setzen loeste gar keinen Aufbau aus — der Test prueft sonst nichts.)
  await page.route('**/api/vehicles*', async (route) => {
    await new Promise((r) => setTimeout(r, 1500));
    return route.continue();
  });

  const startHash = await page.evaluate(() => location.hash);
  expect(startHash).not.toBe('#/vehicles');
  await page.evaluate(() => { location.hash = '#/vehicles'; });
  await page.waitForTimeout(200);           // Aufbau laeuft, Antwort steht aus
  await page.evaluate(() => { location.hash = '#/persons'; });
  await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
  await page.waitForTimeout(2500);          // die verzoegerte Antwort trifft JETZT ein

  const body = await page.locator('#page').innerText();
  expect(body, 'die überholte Route hat die neue Seite überschrieben').not.toContain('Fehler:');
  // Und die Zielseite steht wirklich da.
  await expect(page.locator('.page-head h2')).toHaveText('Personen');
});
