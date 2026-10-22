const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin, persons } = require('./helpers/ui-fixture');

test.use({ serviceWorkers: 'block', locale: 'de-DE', reducedMotion: 'reduce' });

async function open(page, route) {
  await page.goto(origin + '/#/' + route);
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
}

for (const [width, theme] of [[390, 'dark'], [1440, 'light']]) {
  test(`person edit, quick price and GUI restore at ${width}px ${theme}`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ colorScheme: theme });
    await mockUI(page, { role: 'admin', overrides: {
      '/backup/status': { enabled: true, browser_restore: true, settings: {}, status: {}, files: [], s3_files: [] },
    } });
    await open(page, 'persons');
    await page.getByRole('button', { name: /bearbeiten$/ }).first().click();
    const dialog = page.getByRole('dialog', { name: 'Person bearbeiten', exact: true });
    await expect(dialog.locator('.person-danger')).toBeVisible();
    const axe = await new AxeBuilder({ page }).include('#modal').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    expect(axe.violations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('person-edit.png') });
    await page.keyboard.press('Escape');

    await open(page, 'tariffs');
    const summary = page.locator('.quick-price > summary');
    await expect(summary).toBeVisible();
    const metrics = await summary.evaluate(node => {
      const title = node.querySelector('.fold-title').getBoundingClientRect();
      const chev = node.querySelector('.fold-chev').getBoundingClientRect();
      const style = getComputedStyle(node);
      return { display: style.display, listStyle: style.listStyleType, height: node.getBoundingClientRect().height,
        aligned: Math.abs(title.y + title.height / 2 - chev.y - chev.height / 2) < 2 };
    });
    expect(metrics.display).toBe('flex');
    expect(metrics.listStyle).toBe('none');
    expect(metrics.aligned).toBe(true);
    expect(metrics.height).toBeGreaterThanOrEqual(44);
    await page.screenshot({ path: testInfo.outputPath('quick-price.png'), fullPage: true });
    await summary.press('Enter');
    await expect(page.locator('#qp-from')).toBeVisible();
    await expect(page.locator('.quick-price')).toHaveAttribute('open', '');

    await open(page, 'backup');
    await expect(page.getByRole('heading', { name: 'Wiederherstellen', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Validieren', exact: true })).toBeEnabled();
    await expect(page.getByRole('button', { name: 'Wiederherstellen', exact: true })).toBeDisabled();
    await page.screenshot({ path: testInfo.outputPath('gui-restore.png'), fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
  });
}

test('new and already anonymized persons expose only applicable danger actions', async ({ page }) => {
  await mockUI(page, { role: 'editor', overrides: { '/persons': [{ ...persons[0], anonymized: true }] } });
  await open(page, 'persons');
  await page.getByRole('button', { name: '+ Person', exact: true }).click();
  await expect(page.locator('#modal .person-danger')).toHaveCount(0);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: /bearbeiten$/ }).click();
  await expect(page.locator('#f_first_name')).toBeDisabled();
  await expect(page.locator('#modal-submit')).toBeHidden();
  await expect(page.locator('#modal').getByRole('button', { name: 'Anonymisieren (DSGVO)' })).toHaveCount(0);
  await expect(page.locator('#modal').getByRole('button', { name: 'Person löschen' })).toBeVisible();
});

test('successful anonymization closes the editor without saving unsaved form values', async ({ page }) => {
  const requests = await mockUI(page, { role: 'admin' });
  await open(page, 'persons');
  await page.getByRole('button', { name: /bearbeiten$/ }).first().click();
  await page.locator('#f_first_name').fill('Unsaved');
  await page.getByRole('button', { name: 'Anonymisieren (DSGVO)' }).click();
  await page.locator('#confirm-ok').click();
  await expect(page.locator('#modal')).toBeHidden();
  expect(requests.filter(r => r.method !== 'GET').map(r => [r.method, r.path])).toEqual([['POST', '/persons/1/anonymize']]);
});

test('failed anonymization keeps the editor open and preserves unsaved values', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides: {
    '/persons/1/anonymize': route => route.fulfill({ status: 403, contentType: 'application/json', body: JSON.stringify({ error: 'Test: nicht erlaubt' }) }),
  } });
  await open(page, 'persons');
  await page.getByRole('button', { name: /bearbeiten$/ }).first().click();
  await page.locator('#f_first_name').fill('Unsaved');
  await page.getByRole('button', { name: 'Anonymisieren (DSGVO)' }).click();
  await page.locator('#confirm-ok').click();
  await expect(page.locator('#toast')).toContainText('Test: nicht erlaubt');
  await expect(page.locator('#modal')).toBeVisible();
  await expect(page.locator('#f_first_name')).toHaveValue('Unsaved');
});

test('confirmed deletion closes the editor and deletes only after the undo window', async ({ page }) => {
  const requests = await mockUI(page, { role: 'admin' });
  await open(page, 'persons');
  await page.getByRole('button', { name: /bearbeiten$/ }).first().click();
  await page.getByRole('button', { name: 'Person löschen', exact: true }).click();
  await page.locator('#confirm-ok').click();
  await expect(page.locator('#modal')).toBeHidden();
  expect(requests.filter(r => r.method === 'DELETE')).toEqual([]);
  await expect.poll(() => requests.filter(r => r.method === 'DELETE').map(r => r.path), { timeout: 10000 }).toEqual(['/persons/1']);
});

test('GUI restore validates the selected archive and requires revalidation after a key change', async ({ page }) => {
  const requests = await mockUI(page, { role: 'admin', overrides: {
    '/backup/status': { enabled: false, browser_restore: true },
    '/backup/validate': { created: '2026-10-22', entries: 42, confirmation: 'RESTORE abc123' },
  } });
  await open(page, 'backup');
  const card = page.locator('.restore-section');
  await card.locator('input[type=file]').setInputFiles({ name: 'test.dump.enc', mimeType: 'application/octet-stream', buffer: Buffer.from('test archive') });
  await page.getByLabel('Schlüssel', { exact: true }).fill('test-backup-key');
  await page.getByRole('button', { name: 'Validieren', exact: true }).click();
  await expect(card.getByRole('status')).toContainText('Gültiges Backup');
  await expect(page.getByLabel('Prüfsumme bestätigen')).toBeEnabled();
  await expect(page.getByRole('button', { name: 'Wiederherstellen', exact: true })).toBeEnabled();
  await page.getByLabel('Schlüssel', { exact: true }).fill('changed-key');
  await expect(page.getByRole('button', { name: 'Wiederherstellen', exact: true })).toBeDisabled();
  await expect(page.getByLabel('Prüfsumme bestätigen')).toBeDisabled();
  expect(requests.filter(r => r.method === 'POST').map(r => r.path)).toEqual(['/backup/validate']);
});

test('explicitly disabled browser restore retains the offline instructions', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides: {
    '/backup/status': { enabled: true, browser_restore: false, settings: {}, status: {} },
  } });
  await open(page, 'backup');
  await expect(page.getByRole('heading', { name: 'Wiederherstellen · offline' })).toBeVisible();
  await expect(page.locator('.restore-section')).toContainText('PARKRR_BROWSER_RESTORE=true');
  await expect(page.getByRole('button', { name: 'Validieren', exact: true })).toHaveCount(0);
});
