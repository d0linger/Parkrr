// @ts-check
// Verwaltungsansichten: Benutzer sperren/entsperren (API-31) und die
// Mindest-Aufbewahrung im Backup-Zeitplan (Hundert 09).
//
// Die zweite Pruefung braucht ein gesetztes PARKRR_BACKUP_KEY — ohne den Schluessel
// zeigt die Backup-Ansicht nur "Nicht aktiviert" und der Zeitplan wird gar nicht
// gerendert.
const { test, expect } = require('@playwright/test');

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

// Der Hash-Router rendert nur auf hashchange. page.goto('/#/x') von einer bereits
// geladenen Seite feuert das nicht zuverlaessig — deshalb den Hash im Dokument setzen
// und auf das erwartete Element warten.
async function goRoute(page, route, waitFor) {
  await page.evaluate((r) => { location.hash = '#/' + r; }, route);
  await page.waitForSelector(waitFor, { timeout: 15000 });
}

async function login(page) {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
}

test('B8: Konto sperren zeigt das Abzeichen und laesst sich wieder aufheben', async ({ page }) => {
  await login(page);
  const uname = 'b8test' + Date.now();

  // Benutzer ueber die API anlegen (der Test prueft die Sperr-Bedienung, nicht das Formular).
  const created = await page.evaluate(async (u) => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const r = await fetch('/api/users', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: JSON.stringify({ username: u, email: u + '@example.com', role: 'editor', password: 'ein-langes-testpasswort' }),
    });
    return { ok: r.ok, status: r.status, body: await r.text() };
  }, uname);
  expect(created.ok, created.body).toBeTruthy();

  await goRoute(page, 'users', '.u-card');
  const head = page.locator('.u-card .u-head', { hasText: uname }).first();
  await expect(head).toBeVisible();
  await head.click();

  const card = page.locator('.u-card', { hasText: uname }).first();
  const lockBtn = card.getByRole('button', { name: /Zugang sperren/ });
  await expect(lockBtn).toBeVisible();
  await lockBtn.click();

  // Bestaetigungsdialog
  const confirm = page.getByRole('button', { name: /^Sperren$/ });
  await expect(confirm).toBeVisible();
  await confirm.click();

  await expect(page.locator('.u-card', { hasText: uname }).first().locator('.badge-cancelled')).toBeVisible({ timeout: 10000 });

  // Wieder aufheben — ohne Rueckfrage, das ist die harmlose Richtung.
  const card2 = page.locator('.u-card', { hasText: uname }).first();
  await card2.locator('.u-head').click();
  await card2.getByRole('button', { name: /Zugang entsperren/ }).click();
  await expect(page.locator('.u-card', { hasText: uname }).first().locator('.badge-cancelled')).toHaveCount(0, { timeout: 10000 });

  // Aufraeumen
  await page.evaluate(async (u) => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const list = await (await fetch('/api/users', { credentials: 'same-origin' })).json();
    const hit = list.find((x) => x.username === u);
    if (hit) await fetch('/api/users/' + hit.id, { method: 'DELETE', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf } });
  }, uname);
});

test('B8: Zeitplan hat ein Feld fuer die Mindest-Aufbewahrung und speichert es', async ({ page }) => {
  await login(page);
  await goRoute(page, 'backup', '.sched-col');

  const days = page.getByLabel('Mindestalter in Tagen');
  expect(await days.count()).toBe(2); // Volume und S3
  // Der Spaltenkoerper ist eingeklappt, solange "Automatisch" aus ist — dann einschalten.
  if (!(await days.first().isVisible())) {
    await page.locator('.sched-col').first().locator('.sched-switch input').check();
  }
  await expect(days.first()).toBeVisible();

  await days.first().fill('21');
  await page.getByRole('button', { name: 'Zeitplan speichern' }).click();
  await page.waitForTimeout(900);

  const saved = await page.evaluate(async () => {
    const r = await fetch('/api/backup/status', { credentials: 'same-origin' });
    return (await r.json()).settings;
  });
  expect(saved.volume_keep_days).toBe(21);

  // Zuruecksetzen, damit der Testlauf nichts hinterlaesst.
  await days.first().fill('0');
  await page.getByRole('button', { name: 'Zeitplan speichern' }).click();
  await page.waitForTimeout(900);
});
