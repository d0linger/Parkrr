// @ts-check
// Hundert UX-53: Listen sind serverseitig bei 1000 gedeckelt. Bisher sah eine
// abgeschnittene Liste exakt aus wie eine vollstaendige — man blaetterte durch 1000
// Personen und hielt das fuer alle. Der Server schickt X-Total-Count laengst; jetzt
// liest ihn das Frontend und sagt es.
const { test, expect } = require('@playwright/test');
test.setTimeout(60000);

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

test('Abgeschnittene Liste sagt, dass sie abgeschnitten ist', async ({ page }) => {
  await login(page);

  // Der Kopf muss zuerst ueberhaupt ankommen — sonst prueft der Test seine eigene
  // Annahme statt der Anzeige.
  const head = await page.evaluate(async () => {
    const r = await fetch('/api/persons?limit=1', { credentials: 'same-origin' });
    return { total: r.headers.get('X-Total-Count'), n: (await r.json()).length };
  });
  expect(Number(head.total), 'X-Total-Count fehlt auf /persons').toBeGreaterThan(1);
  expect(head.n).toBe(1);

  await page.goto('/#/persons');
  await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
  // Vollstaendige Liste: KEIN Hinweis.
  await expect(page.locator('.list-trunc')).toBeHidden();

  // Jetzt den Deckel echt herbeifuehren: die Anfrage der Anwendung wird auf limit=1
  // umgebogen. Die Antwort traegt weiterhin die WAHRE Gesamtzahl im Kopf — genau die
  // Lage, in der eine Installation mit ueber 1000 Personen dauerhaft waere.
  await page.route('**/api/persons', (route) => {
    const u = new URL(route.request().url());
    u.searchParams.set('limit', '1');
    return route.continue({ url: u.toString() });
  });
  await page.goto('/#/dashboard');
  await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
  await page.goto('/#/persons');
  await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });

  const note = page.locator('.list-trunc');
  await expect(note).toBeVisible();
  const text = (await note.textContent()) || '';
  expect(text).toContain('1 von ' + head.total);
  expect(text).toContain('Suche');
});

test('Auch Gefährte und Zusatzkosten melden ihre Gesamtzahl', async ({ page }) => {
  await login(page);
  for (const path of ['/api/vehicles', '/api/charges']) {
    const total = await page.evaluate(async (p) => {
      const r = await fetch(p + '?limit=1', { credentials: 'same-origin' });
      return r.headers.get('X-Total-Count');
    }, path);
    expect(total, path + ' meldet keine Gesamtzahl').not.toBeNull();
  }
});
