const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const path = require('node:path');
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

test.use({ serviceWorkers: 'block' });

for (const width of [390, 1440]) {
  test(`tax-year workspace is usable at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await mockUI(page, { role: 'admin', overrides });
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));

    await page.goto(origin + '/#/taxyear');
    await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
    await expect(page.locator('.route-view h2').getByText('Steuerjahr', { exact: true })).toBeVisible();
    await expect(page.getByText('Überschuss 2025')).toBeVisible();
    await expect(page.getByRole('link', { name: /Komplettes Exportpaket/ })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Kleinunternehmer-Grenze' })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(errors).toEqual([]);

    if (process.env.PARKRR_CAPTURE_DIR) {
      await page.screenshot({ path: path.join(process.env.PARKRR_CAPTURE_DIR, `taxyear-${width}.png`), fullPage: true });
    }

    const results = await new AxeBuilder({ page }).include('.route-view').withTags(['wcag22a', 'wcag22aa']).analyze();
    expect(results.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
  });
}

test('tax-year entry workflows expose their required controls', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides });
  await page.goto(origin + '/#/taxyear');

  await page.getByText('+ Ausgabe erfassen', { exact: true }).click();
  await expect(page.getByPlaceholder('z. B. Wartung Hallentor')).toBeVisible();
  await expect(page.getByLabel('Beleg (PDF/JPEG/PNG)', { exact: true })).toBeVisible();

  await page.getByText(/Wiederkehrende Ausgaben/).click();
  await expect(page.getByRole('button', { name: 'Vorlage anlegen' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Deaktivieren' })).toBeVisible();

  await page.getByText(/Anlageverzeichnis/).click();
  await expect(page.getByLabel('Nutzungsdauer (Jahre)', { exact: true })).toBeVisible();
  await expect(page.getByText('Halbjahresregel anwenden', { exact: true })).toBeVisible();

  await expect(page.getByText('Kennzahl 9460 · Einnahmen').first()).toBeVisible();
  await page.getByText(/Steuerobjekte verwalten/).click();
  await page.getByRole('button', { name: 'Bearbeiten' }).first().click();
  await expect(page.getByRole('button', { name: 'Steuerobjekt speichern' })).toBeVisible();
  await expect(page.getByLabel('Einheitswert-Aktenzeichen')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Jahr abschließen' })).toBeVisible();
});

test('expense receipt retry does not create a duplicate expense', async ({ page }) => {
  let expenseCreates = 0;
  let receiptUploads = 0;
  await mockUI(page, { role: 'admin', overrides: {
    ...overrides,
    '/tax/expenses': async route => {
      expenseCreates++;
      await route.fulfill({ status: 201, contentType: 'application/json', body: JSON.stringify({ id: 99 }) });
    },
    '/tax/expenses/99/receipts': async route => {
      receiptUploads++;
      await route.fulfill({
        status: receiptUploads === 1 ? 500 : 201,
        contentType: 'application/json',
        body: JSON.stringify(receiptUploads === 1 ? { error: 'Upload fehlgeschlagen' } : { id: 7 }),
      });
    },
  } });
  await page.goto(origin + '/#/taxyear');

  const entry = page.locator('details').filter({ hasText: '+ Ausgabe erfassen' });
  await entry.locator('summary').click();
  await entry.getByPlaceholder('z. B. Wartung Hallentor').fill('Retry-Test');
  await entry.getByLabel('Betrag brutto', { exact: true }).fill('10');
  await entry.getByLabel('Beleg (PDF/JPEG/PNG)', { exact: true }).setInputFiles({
    name: 'rechnung.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n%%EOF\n'),
  });

  await entry.getByRole('button', { name: 'Ausgabe buchen' }).click();
  await expect(entry.getByRole('button', { name: 'Beleg erneut hochladen' })).toBeEnabled();
  expect(expenseCreates).toBe(1);
  expect(receiptUploads).toBe(1);

  await entry.getByRole('button', { name: 'Beleg erneut hochladen' }).click();
  await expect.poll(() => receiptUploads).toBe(2);
  expect(expenseCreates).toBe(1);
});
