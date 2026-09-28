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

    const results = await new AxeBuilder({ page }).include('.route-view').analyze();
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
