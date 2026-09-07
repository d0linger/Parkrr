// @ts-check
// Hundert 62: die Diagramme sind tastatur- und screenreader-tauglich. Vorher waren
// sie reine Hover-Flächen mit dem aria-label "Verlauf" — ohne Maus keine Werte.
const { test, expect } = require('@playwright/test');
test.setTimeout(60000);

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

test('Charts: beschreibendes Label, sr-Tabelle, Pfeiltasten-Navigation', async ({ page }) => {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  // Ohne Daten navigiert die Tastatur bewusst nichts (lastPositive < 0) — der Test
  // legt deshalb eine Zahlung im laufenden Jahr an, damit der Umsatz-Chart Werte hat.
  await page.evaluate(async () => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const p = await (await fetch('/api/persons', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ first_name: 'Chart', last_name: 'A11ySeed' }),
    })).json();
    // Ein Einzelposten im laufenden Jahr speist Umsatz- UND Zusatzkosten-Chart
    // (der Umsatz ist Abgrenzung + Posten, nicht Zahlungseingang).
    await fetch('/api/charges', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ person_id: p.id, description: 'Chart-Seed', amount: 123.45, quantity: 1 }),
    });
    window.__chartSeedPerson = p.id;
  });
  await page.reload();
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
  await page.waitForSelector('.chart svg', { timeout: 30000 });

  const chart = page.locator('.chart').first();
  // 1) Das Label trägt die Kernzahlen, nicht ein Gattungswort.
  const label = await chart.getAttribute('aria-label');
  expect(label, 'aria-label fehlt').toBeTruthy();
  expect(label).not.toBe('Verlauf');
  expect(label).toContain('Umsatz');

  // 2) sr-only-Datentabelle mit einer Zeile je Monat.
  const rows = await chart.locator('table.sr-only tr').count();
  expect(rows).toBe(12);

  // 3) Tastatur: fokussieren, Pfeil rechts — der Tooltip erscheint und die
  //    Live-Region sagt den Wert an.
  await chart.focus();
  await page.keyboard.press('ArrowRight');
  await expect(chart.locator('.c-tip')).toHaveCSS('opacity', '1');
  const live = await chart.locator('[aria-live]').textContent();
  expect(live).toMatch(/€/);
  // Ende springt zum letzten Wert.
  await page.keyboard.press('End');
  const live2 = await chart.locator('[aria-live]').textContent();
  expect(live2).toMatch(/€/);

  // Aufräumen: Zahlung + Person wieder entfernen (Zahlungen kaskadieren mit).
  await page.evaluate(async () => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const list = await (await fetch('/api/persons', { credentials: 'same-origin' })).json();
    for (const p of list.filter((x) => x.last_name === 'A11ySeed')) {
      await fetch('/api/persons/' + p.id, { method: 'DELETE', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf } });
    }
  });
});
