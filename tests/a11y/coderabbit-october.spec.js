const { test, expect } = require('@playwright/test');
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

test.use({ serviceWorkers: 'block', locale: 'de-DE', timezoneId: 'Europe/Vienna', reducedMotion: 'reduce' });

async function open(page, route) {
  await page.goto(origin + '/#/' + route);
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
}

for (const failure of ['server', 'network']) {
  test(`failed invoice mailing retry stays usable after ${failure} failure`, async ({ page }) => {
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    let attempts = 0;
    await mockUI(page, { role: 'editor', overrides: { ...overrides,
      '/invoices/1/mailings': [{ id: 7, kind: 'invoice', recipient: 'test@example.invalid', status: 'failed', attempts: 6 }],
      '/mail-outbox/7/retry': async route => {
        attempts++;
        if (attempts === 1) {
          if (failure === 'network') return route.abort('failed');
          return route.fulfill({ status: 503, json: { error: 'Versand vorübergehend nicht verfügbar' } });
        }
        return route.fulfill({ json: { queued: true } });
      },
    } });
    await open(page, 'invoices/1');
    const retry = page.getByRole('button', { name: 'Erneut senden', exact: true });
    await retry.click();
    await expect(page.locator('#toast')).toHaveClass(/error/);
    await expect(retry).toBeEnabled();
    await retry.click();
    await expect.poll(() => attempts).toBe(2);
    expect(errors).toEqual([]);
  });
}

for (const outcome of ['success', 'failure', 'cancel']) {
  test(`invoice preview closes only after successful creation: ${outcome}`, async ({ page }) => {
    let writes = 0;
    await mockUI(page, { role: 'editor', overrides: { ...overrides,
      '/invoices/preview': { total: 85, as_of: '2026-10-22T10:00:00Z', persons: [
        { person_id: 1, name: 'Demo Berger', total: 85, warnings: [], lines: [{ label: 'Miete', amount: 85 }] },
      ] },
      '/persons/1/invoices': route => {
        writes++;
        return route.fulfill(outcome === 'failure'
          ? { status: 503, json: { error: 'Rechnung konnte nicht erstellt werden' } }
          : { status: 201, json: { id: 1, number: '2026-0012' } });
      },
    } });
    await open(page, 'invoices');
    await page.getByRole('button', { name: 'Vorschau Rechnungslauf', exact: true }).click();
    const preview = page.getByRole('dialog', { name: 'Vorschau Rechnungslauf', exact: true });
    await preview.locator('summary').click();
    await preview.getByRole('button', { name: 'Rechnung erstellen', exact: true }).click();
    if (outcome === 'cancel') {
      await page.keyboard.press('Escape');
      await expect(preview).toBeVisible();
      expect(writes).toBe(0);
      return;
    }
    await page.locator('#confirm-ok').click();
    if (outcome === 'failure') {
      await expect(page.locator('#toast')).toContainText('Rechnung konnte nicht erstellt werden');
      await expect(preview).toBeVisible();
      await expect(preview.getByRole('button', { name: 'Rechnung erstellen', exact: true })).toBeEnabled();
    } else {
      await expect(page).toHaveURL(/#\/invoices\/1$/);
      await expect(page.locator('#modal')).toBeHidden();
      await expect(page.locator('.invoice-doc')).toBeVisible();
    }
    expect(writes).toBe(1);
  });
}

for (const role of ['hallendienst', 'editor']) {
  test(`planner tools respect ${role} permissions`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await mockUI(page, { role, overrides });
    await open(page, 'hall/1');
    const icons = page.getByRole('button', { name: 'Eigene Planer-Icons verwalten', exact: true });
    const templates = page.getByRole('button', { name: 'Wand-Layouts speichern & wiederverwenden', exact: true });
    if (role === 'hallendienst') {
      await expect(icons).toHaveCount(0);
      await expect(templates).toHaveCount(0);
      await expect(page.locator('.gp-vpick').first()).toBeVisible();
    } else {
      await expect(icons).toBeVisible();
      await expect(templates).toBeVisible();
    }
  });
}

for (const [endDate, expected] of [['2026-11-01', 1], ['2026-11-01T00:00:00Z', 1], ['invalid', 0]]) {
  test(`placement urgency handles end date ${endDate}`, async ({ page }) => {
    await page.clock.setFixedTime(new Date('2026-10-22T10:00:00Z'));
    await page.addInitScript(() => {
      let geometry;
      window.__urgencies = [];
      Object.defineProperty(window, 'PG', { configurable: true,
        get: () => geometry,
        set: value => {
          geometry = value;
          const suggest = value.suggestPlacements;
          value.suggestPlacements = (size, options) => {
            window.__urgencies.push(options.urgency);
            return suggest(size, options);
          };
        },
      });
    });
    const palette = overrides['/vehicles/unassigned'].map((v, i) => i === 0 ? { ...v, end_date: endDate } : v);
    await mockUI(page, { role: 'editor', overrides: { ...overrides, '/vehicles/unassigned': palette } });
    await open(page, 'hall/1');
    const pick = page.locator('.gp-vehc', { hasText: palette[0].label }).locator('.gp-vpick');
    await pick.press('Enter');
    await expect(pick).toHaveAttribute('aria-pressed', 'true');
    await page.getByRole('button', { name: 'Platz vorschlagen', exact: true }).click();
    await expect.poll(() => page.evaluate(() => window.__urgencies)).toEqual([expected]);
    await expect(page.locator('.gp-suggest').first()).toBeVisible();
  });
}
