const { test, expect } = require('@playwright/test');

const isolated = process.env.PARKRR_E2E_ISOLATED === '1' &&
  ['http://localhost:18099', 'http://localhost:18101'].includes(process.env.PARKRR_BASE_URL);
const enabled = process.env.PARKRR_E2E_RESTORE === '1';
test.skip(!isolated || !enabled, 'Destructive restore requires an explicitly disposable E2E server.');

test('GUI restore recovers exact invoice, payment and balance values and revokes sessions', async ({ page, context }, testInfo) => {
  test.setTimeout(180000);
  const password = process.env.PARKRR_E2E_PASS;
  const backupKey = process.env.PARKRR_E2E_BACKUP_KEY;
  expect(password, 'E2E account password must be supplied').toBeTruthy();
  expect(backupKey, 'E2E archive key must be supplied').toBeTruthy();
  const login = async () => {
    await page.goto('/');
    await page.locator('#login-username').fill(process.env.PARKRR_E2E_USER || 'admin');
    await page.locator('#login-password').fill(password);
    await page.locator('#login-form button[type="submit"]').click();
    await expect(page.locator('#app-view')).toBeVisible();
  };
  await login();
  let headers;
  const refreshCSRF = async () => {
    const csrf = (await context.cookies()).find(cookie => cookie.name === 'parkrr_csrf');
    expect(csrf).toBeDefined();
    headers = { 'X-CSRF-Token': csrf.value };
  };
  await refreshCSRF();
  const get = async path => {
    const response = await page.request.get('/api' + path);
    expect(response.ok(), path + ': ' + await response.text()).toBe(true);
    return response.json();
  };
  const post = async (path, data) => {
    const response = await page.request.post('/api' + path, { headers, data });
    expect(response.ok(), path + ': ' + await response.text()).toBe(true);
    return response.json();
  };
  const billing = await get('/billing/settings');
  await post('/billing/settings', { ...billing, seller_name: 'Restore Testbetrieb', seller_address: 'Teststraße 1, 1010 Wien', kleinunternehmer: true });
  const person = await post('/persons', { first_name: 'Restore', last_name: 'Wertecheck', address: 'Teststraße 2, 1010 Wien' });
  await post('/charges', { person_id: person.id, description: 'Rundungstest', amount: 123.45, quantity: 2.5 });
  const invoice = await post('/persons/' + person.id + '/invoices', {});
  expect(invoice.total).toBe(308.63);
  await post('/persons/' + person.id + '/pay-invoices', { amount: 30.12, method: 'bar', auto: true });
  const snapshot = async () => ({
    stats: await get('/persons/' + person.id + '/stats'),
    invoice: await get('/invoices/' + invoice.id),
    payments: await get('/persons/' + person.id + '/payments'),
    charges: await get('/charges?person_id=' + person.id),
    billing: await get('/billing/settings'),
  });
  const before = await snapshot();
  expect(before.invoice.paid_amount).toBe(30.12);
  expect(before.stats.balance).toBe(278.51);
  await page.goto('/#/backup');
  await expect(page.getByRole('heading', { name: 'Wiederherstellen', exact: true })).toBeVisible();
  const downloading = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Sofort herunterladen', exact: true }).click();
  const download = await downloading;
  const archive = testInfo.outputPath('restore-values.dump.enc');
  await download.saveAs(archive);

  await post('/persons/' + person.id + '/pay-invoices', { amount: 100.01, method: 'bar', auto: true });
  await post('/charges', { person_id: person.id, description: 'Nach dem Backup', amount: 88.88, quantity: 1 });
  const changed = await snapshot();
  expect(changed.invoice.paid_amount).toBe(130.13);
  expect(changed.stats.balance).not.toBe(before.stats.balance);
  await page.locator('.restore-section input[type=file]').setInputFiles(archive);
  await page.getByLabel('Schlüssel', { exact: true }).fill(backupKey);
  await page.getByRole('button', { name: 'Validieren', exact: true }).click();
  const confirmation = page.getByLabel('Prüfsumme bestätigen');
  await expect(confirmation).toBeEnabled({ timeout: 30000 });
  await confirmation.fill(await confirmation.getAttribute('placeholder'));
  const queued = page.waitForResponse(response => new URL(response.url()).pathname === '/api/backup/restore' && response.request().method() === 'POST');
  await page.getByRole('button', { name: 'Wiederherstellen', exact: true }).click();
  await page.locator('#confirm-ok').click();
  expect((await queued).status()).toBe(202);
  await expect(page.locator('#login-view')).toBeVisible({ timeout: 90000 });
  expect((await page.request.get('/api/auth/me')).status()).toBe(401);
  await login();
  await refreshCSRF();
  expect(await snapshot()).toEqual(before);
});
