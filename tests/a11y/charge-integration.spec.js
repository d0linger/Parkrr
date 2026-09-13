const path = require('node:path');
const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

test.use({ serviceWorkers: 'block' });
test.beforeEach(async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.clock.setFixedTime(new Date('2026-09-10T12:00:00Z'));
});
const openCharge = async page => {
  await page.goto(origin + '/#/finance');
  await page.getByRole('button', { name: '+ Neu', exact: true }).click();
  await expect(page.locator('#modal')).toBeVisible();
};

test('optional end date validates while collapsed, preserves values and submits recurring payload', async ({ page }) => {
  const writes = [];
  await mockUI(page, { overrides: { ...overrides, '/persons/1/recurring': route => {
    writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 7 } });
  } } });
  await openCharge(page);
  await page.locator('#f_description').fill('Strom');
  await page.locator('#f_amount').fill('20');
  await page.locator('#f_quantity').fill('2.5');
  await page.getByRole('button', { name: 'monatlich', exact: true }).click();
  await expect(page.locator('#f_end_date')).toBeHidden();
  await expect(page.locator('#f_quantity')).toBeDisabled();
  await page.getByText('Weitere Angaben', { exact: true }).click();
  await page.locator('#f_vehicle_id').selectOption('1');
  await page.locator('#f_end_date').fill('2026-09-01');
  await page.getByText('Weitere Angaben', { exact: true }).click();
  await expect(page.locator('.form-disclosure summary')).toContainText('2 Angaben');
  await page.locator('#modal-submit').click();
  await expect(page.locator('#f_end_date')).toBeFocused();
  await expect(page.locator('#err_end_date')).toBeVisible();
  expect(writes).toEqual([]);
  await page.locator('#f_end_date').fill('2026-12-31');
  await expect(page.locator('#err_end_date')).toBeHidden();
  await page.getByText('Weitere Angaben', { exact: true }).click();
  await page.locator('#modal-submit').click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toEqual({ description: 'Strom', amount: 20, period: 'monthly', start_date: '2026-09-10', end_date: '2026-12-31', vehicle_id: 1 });
});

test('switching modes restores quantity and ignores inactive invalid end date', async ({ page }) => {
  const writes = [];
  await mockUI(page, { overrides: { ...overrides, '/charges': route => {
    if (route.request().method() === 'GET') return route.fulfill({ json: [] });
    writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 7 } });
  } } });
  await openCharge(page);
  // Use the actual native catalog value (currency spacing is locale-specific).
  await page.locator('#f_description').fill(await page.locator('#charge-services option').first().getAttribute('value'));
  await page.locator('#f_quantity').fill('2.5');
  await page.getByRole('button', { name: 'jährlich', exact: true }).click();
  await expect(page.locator('#charge-total')).toContainText('45,00');
  await page.getByText('Weitere Angaben', { exact: true }).click();
  await page.locator('#f_end_date').fill('2025-01-01');
  await page.getByRole('button', { name: 'einmalig', exact: true }).click();
  await expect(page.locator('#f_quantity')).toHaveValue('2.5');
  await expect(page.locator('#f_end_date')).toBeDisabled();
  await expect(page.locator('.form-disclosure-count')).toBeHidden();
  await expect(page.locator('#charge-total')).toContainText('112,50');
  await page.getByRole('button', { name: 'Gestern', exact: true }).click();
  await page.locator('#modal-submit').click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toEqual({ person_id: 1, description: 'Innenreinigung', amount: 45, quantity: 2.5, charged_on: '2026-09-09', vehicle_id: null });
});

test('oversized totals block submission with recoverable inline validation', async ({ page }) => {
  await mockUI(page, { overrides });
  await openCharge(page);
  await page.locator('#f_description').fill('Test');
  await page.locator('#f_amount').fill('100000000000000');
  await expect(page.locator('#charge-total')).toHaveText('–');
  await page.locator('#modal-submit').click();
  await expect(page.locator('#f_amount')).toBeFocused();
  await expect(page.locator('#err_amount')).toContainText('zu groß');
  await page.locator('#f_amount').fill('0');
  await expect(page.locator('#charge-total')).toContainText('0,00');
  await expect(page.locator('#err_amount')).toBeHidden();
  expect(await page.locator('#f_amount').evaluate(node => node.checkValidity())).toBe(true);
});

test('recurring edit uses the same preview and preserves existing optional fields', async ({ page }) => {
  const writes = [];
  const recurring = { id: 7, person_id: 1, description: 'Strom', amount: 20, period: 'monthly', start_date: '2026-09-01', end_date: '2026-12-31', vehicle_id: 1 };
  await mockUI(page, { overrides: { ...overrides,
    '/persons/1/recurring': [recurring],
    '/persons/1/stats': { ...overrides['/persons/1/stats'], recurring_charges: [recurring] },
    '/recurring/7': route => { writes.push(route.request().postDataJSON()); return route.fulfill({ json: recurring }); },
  } });
  await page.goto(origin + '/#/persons/1');
  await page.getByRole('button', { name: 'Strom bearbeiten', exact: true }).click();
  await expect(page.locator('#f_end_date')).toBeVisible();
  await expect(page.locator('.form-disclosure summary')).toContainText('2 Angaben');
  await expect(page.locator('#charge-total')).toContainText('20,00');
  await page.locator('#f_period').selectOption('yearly');
  await page.locator('#f_amount').fill('240');
  await expect(page.locator('.form-total')).toContainText('Betrag pro Jahr');
  await page.getByText('Weitere Angaben', { exact: true }).click();
  await page.locator('#modal-submit').click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toEqual({ description: 'Strom', amount: 240, period: 'yearly', start_date: '2026-09-01', end_date: '2026-12-31', vehicle_id: 1 });
});

for (const scenario of ['active', 'archived', 'unavailable', 'unbound']) {
  test(`editing a charge restores its ${scenario} vehicle binding after switching person`, async ({ page }) => {
    const writes = [];
    const charge = { ...overrides['/charges'][2], vehicle_id: scenario === 'unbound' ? null : 1 };
    const fleet = [
      { ...overrides['/vehicles'][0], archived: scenario === 'archived' },
      overrides['/vehicles'][1],
      { ...overrides['/vehicles'][0], id: 99, archived: true },
    ];
    let targetLoads = 0;
    await mockUI(page, { overrides: { ...overrides,
      '/charges': [charge],
      '/charges/3': route => {
        expect(route.request().method()).toBe('PUT');
        writes.push(route.request().postDataJSON());
        return route.fulfill({ json: charge });
      },
      '/vehicles': route => {
        const person = new URL(route.request().url()).searchParams.get('person_id');
        if (person === '1' && ++targetLoads > 1 && scenario === 'unavailable') {
          return route.fulfill({ status: 503, json: { error: 'Test: vehicle list unavailable' } });
        }
        return route.fulfill({ json: person ? fleet.filter(v => String(v.person_id) === person) : fleet });
      },
    } });
    await page.goto(origin + '/#/finance');
    await page.getByRole('button', { name: 'Batterieservice bearbeiten', exact: true }).click();
    if (scenario === 'unbound') await page.getByText('Weitere Angaben', { exact: true }).click();
    const person = page.locator('#f_person_id');
    const vehicle = page.locator('#f_vehicle_id');
    const expected = charge.vehicle_id == null ? '' : String(charge.vehicle_id);
    await expect(vehicle).toHaveValue(expected);
    await person.selectOption('2');
    await expect(vehicle).toBeEnabled();
    await expect.poll(() => vehicle.locator('option').evaluateAll(options => options.map(o => o.value))).toEqual(['', '2']);
    await expect(vehicle).toHaveValue('');
    await person.selectOption('1');
    await expect(vehicle).toBeEnabled();
    await expect(vehicle).toHaveValue(expected);
    await expect(vehicle.locator('option[value="99"]')).toHaveCount(0);
    if (scenario === 'unavailable') await expect(vehicle).toContainText('Liste nicht geladen');
    await page.locator('#modal-submit').click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0]).toEqual({ person_id: 1, description: charge.description, amount: charge.amount,
      quantity: charge.quantity, charged_on: charge.charged_on, vehicle_id: charge.vehicle_id });
  });
}

for (const [width, theme] of [[1440, 'light'], [390, 'dark'], [320, 'light']]) {
  test(`charge layout and keyboard ${width} ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ colorScheme: theme });
    await mockUI(page, { overrides });
    await openCharge(page);
    await page.locator('#f_amount').fill('45');
    const positions = await page.evaluate(() => Object.fromEntries(['f_amount', 'f_quantity', 'charge-total', 'f_charged_on', 'modal-submit'].map(id => {
      const { x, y, bottom } = document.getElementById(id).getBoundingClientRect(); return [id, { x, y, bottom }];
    })));
    expect(positions['f_amount'].y).toBe(positions['f_quantity'].y);
    expect(positions['charge-total'].bottom).toBeLessThan(positions['f_charged_on'].y);
    expect(positions['charge-total'].bottom).toBeLessThan(positions['modal-submit'].y);
    if (width === 1440) expect(positions['charge-total'].x).toBeGreaterThan(positions['f_quantity'].x);
    expect(await page.locator('#modal-body').evaluate(node => node.scrollWidth <= node.clientWidth)).toBe(true);
    await page.locator('.form-disclosure summary').focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#f_vehicle_id')).toBeVisible();
    await page.keyboard.press('Enter');
    const result = await new AxeBuilder({ page }).include('#modal').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
    await page.screenshot({ path: path.resolve(__dirname, `../../.impeccable/review/charge-integration/after-${width}-${theme}.png`) });
  });
}
