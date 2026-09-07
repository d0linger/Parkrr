// @ts-check
// Hundert 78: die Symbol-Knoepfe tragen ein SVG statt eines Emojis. Der Test prueft,
// dass dort WIRKLICH ein Icon steht — ein fehlender ICONS-Schluessel erzeugt sonst
// einen leeren Knopf, und leere Knoepfe sind in diesem Projekt schon einmal
// unbemerkt ausgeliefert worden.
const { test, expect } = require('@playwright/test');
test.setTimeout(60000);

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

test('Icon-Knöpfe sind gefüllt, nicht leer', async ({ page }) => {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  // Die Export-Karte steht auf der ÜBERSICHT (routes.dashboard), nicht unter
  // #/finance. Dass diese Prüfung je unter #/finance grün war, lag am inzwischen
  // behobenen Render-Wettlauf: der noch laufende Dashboard-Aufbau überschrieb die
  // Finanzseite samt seiner Export-Links — der Test fand sie auf der falschen Route.
  await page.reload();
  await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });

  // Die Export-Knöpfe trugen ein ⭳-Zeichen und tragen jetzt icon('download').
  const exp = page.locator('a[href="/api/export/invoices"]');
  await expect(exp).toHaveCount(1);
  const info = await exp.evaluate((a) => ({
    svgs: a.querySelectorAll('svg').length,
    paths: a.querySelectorAll('svg path, svg circle, svg line, svg rect').length,
    text: a.textContent.trim(),
  }));
  expect(info.svgs, 'kein SVG im Export-Knopf').toBe(1);
  expect(info.paths, 'das SVG ist leer — fehlender ICONS-Schlüssel').toBeGreaterThan(0);
  expect(info.text).toContain('Rechnungen');

  // Und es ist kein Emoji mehr übrig.
  expect(info.text).not.toContain('⭳');
});
