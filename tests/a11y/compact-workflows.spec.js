const path = require('node:path');
const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin, persons } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

test.use({ serviceWorkers: 'block' });
test.beforeEach(async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.clock.setFixedTime(new Date('2026-09-10T12:00:00Z'));
  await page.addInitScript(() => {
    localStorage.setItem('parkrr_portal_lang', 'de');
    localStorage.setItem('parkrr-veh', '0');
  });
});
const openNew = async (page, route) => {
  await page.goto(origin + '/#/' + route);
  await page.getByRole('button', { name: '+ Neu', exact: true }).click();
  await expect(page.locator('#modal')).toBeVisible();
};
const save = async (page, writes) => {
  await page.locator('#modal-submit').click();
  await expect.poll(() => writes.length).toBe(1);
  await expect(page.locator('#modal')).toBeHidden();
};

test('person fields pair on desktop; closed optional fields retain their exact payload', async ({ page }) => {
  const writes = [];
  await page.setViewportSize({ width: 1440, height: 900 });
  await mockUI(page, { overrides: { ...overrides, '/persons': route => {
    if (route.request().method() === 'GET') return route.fulfill({ json: persons });
    writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 25 } });
  } } });
  await openNew(page, 'persons');
  await expect(page.getByLabel('Vorname', { exact: true })).toBeFocused();
  await expect(page.getByLabel('Adresse', { exact: true })).toBeHidden();
  const [first, last] = await page.evaluate(() => ['f_first_name', 'f_last_name'].map(id => {
    const { x, y } = document.getElementById(id).getBoundingClientRect(); return { x, y };
  }));
  expect(first.y).toBe(last.y); expect(last.x).toBeGreaterThan(first.x);
  const modal = await page.locator('#modal').boundingBox();
  expect(modal.height).toBeLessThan(500);
  await page.getByLabel('Vorname', { exact: true }).fill('Ada');
  await page.getByLabel('Nachname', { exact: true }).fill('Muster');
  await page.getByText('Adresse & Notizen', { exact: true }).click();
  await page.getByLabel('Adresse', { exact: true }).fill('Wien\nTestgasse 1');
  await page.getByLabel('Notizen', { exact: true }).fill('Nicht verlieren');
  await page.getByText('Adresse & Notizen', { exact: true }).click();
  await save(page, writes);
  expect(writes[0]).toEqual({ first_name: 'Ada', last_name: 'Muster', email: '', phone: '', address: 'Wien\nTestgasse 1', notes: 'Nicht verlieren' });
});

test('editing a person reveals stored additional data', async ({ page }) => {
  await mockUI(page, { overrides: { ...overrides, '/persons': [{ ...persons[0], address: 'Bestehende Adresse', notes: 'Bestehende Notiz' }] } });
  await page.goto(origin + '/#/persons');
  await page.getByRole('button', { name: 'Demo Berger 1 bearbeiten', exact: true }).click();
  await expect(page.getByLabel('Adresse', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Notizen', { exact: true })).toHaveValue('Bestehende Notiz');
});

test('validation reopens a closed password group and focuses the error', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides });
  await page.goto(origin + '/#/users');
  await page.locator('.u-head').first().click();
  await page.locator('.u-edit').first().click();
  const summary = page.getByText('Passwort ändern', { exact: true });
  await summary.click();
  await page.getByLabel('Neues Passwort (optional)', { exact: true }).fill('short');
  await summary.click();
  await page.locator('#modal-submit').click();
  await expect(page.getByLabel('Neues Passwort (optional)', { exact: true })).toBeFocused();
  await expect(page.locator('#err_password')).toBeVisible();
});

test('catalog autocomplete, custom description, total and date shortcuts preserve the charge contract', async ({ page }) => {
  const writes = [];
  await mockUI(page, { overrides: { ...overrides, '/charges': route => {
    if (route.request().method() === 'GET') return route.fulfill({ json: [] });
    writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 7 } });
  } } });
  await openNew(page, 'finance');
  const suggestion = await page.locator('#charge-services option').first().getAttribute('value');
  await page.locator('#f_description').fill(suggestion);
  await expect(page.locator('#f_description')).toHaveValue('Innenreinigung');
  await expect(page.locator('#f_amount')).toHaveValue('45');
  await page.locator('#f_description').fill('Individuelle Reinigung');
  await page.getByLabel('Menge', { exact: true }).fill('2.5');
  await expect(page.locator('#charge-total')).toHaveText(/112,50/);
  await page.getByRole('button', { name: 'Gestern', exact: true }).click();
  await expect(page.getByLabel('Datum', { exact: true })).toHaveValue('2026-09-09');
  await save(page, writes);
  expect(writes[0]).toEqual({ person_id: 1, description: 'Individuelle Reinigung', amount: 45, quantity: 2.5, charged_on: '2026-09-09', vehicle_id: null });
});

test('recurring mode hides complete field wrappers and ignores one-off quantity', async ({ page }) => {
  const writes = [];
  await mockUI(page, { overrides: { ...overrides, '/persons/1/recurring': route => {
    writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 2 } });
  } } });
  await openNew(page, 'finance');
  await page.locator('#f_description').fill('Strom');
  await page.locator('#f_amount').fill('20');
  await page.getByLabel('Menge', { exact: true }).fill('3');
  await page.getByRole('button', { name: 'monatlich', exact: true }).click();
  await expect(page.locator('#f_quantity').locator('..')).toBeHidden();
  await expect(page.getByLabel('Gültig ab', { exact: true })).toBeVisible();
  await expect(page.locator('#charge-total')).toHaveText(/20,00/);
  await page.getByText('Weitere Angaben', { exact: true }).click();
  await page.getByLabel('Zuordnung (optional)', { exact: true }).selectOption('1');
  await page.getByText('Weitere Angaben', { exact: true }).click();
  await save(page, writes);
  expect(writes[0]).toEqual({ description: 'Strom', amount: 20, period: 'monthly', start_date: '2026-09-10', end_date: null, vehicle_id: 1 });
});

test('vehicle rate follows the tariff only until manually changed; optional settings survive closing', async ({ page }) => {
  const writes = [];
  await mockUI(page, { overrides: { ...overrides, '/vehicles': route => {
    if (route.request().method() === 'GET') return route.fulfill({ json: overrides['/vehicles'] });
    writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 99 } });
  } } });
  await openNew(page, 'vehicles');
  await expect(page.locator('#f_rate')).toHaveValue('85');
  await page.getByRole('button', { name: 'jährlich', exact: true }).click();
  await expect(page.locator('#f_rate')).toHaveValue('1020');
  await page.locator('#f_rate').fill('0');
  await page.getByRole('button', { name: 'monatlich', exact: true }).click();
  await expect(page.locator('#f_rate')).toHaveValue('0');
  await page.getByText('Notizen & Planer-Details', { exact: true }).click();
  await page.getByLabel('Notizen', { exact: true }).fill('Strom benötigt');
  await page.getByRole('checkbox', { name: /Ladebedarf/ }).focus();
  await page.keyboard.press('Space');
  await expect(page.getByRole('checkbox', { name: /Ladebedarf/ })).toBeChecked();
  await page.getByLabel('Planer-Symbol', { exact: true }).selectOption('PKW');
  await page.getByText('Notizen & Planer-Details', { exact: true }).click();
  await save(page, writes);
  expect(writes[0]).toEqual({ person_id: 1, category_id: 1, label: '', license_plate: '', notes: 'Strom benötigt', billing_period: 'monthly',
    rate: 0, start_date: '2026-09-10', status: 'stored', end_date: null, reserved_from: null, reserved_until: null, needs_power: true, planner_symbol: 'PKW' });
});

test('editing a charge retains its vehicle when the vehicle lookup fails', async ({ page }) => {
  const writes = [];
  await mockUI(page, { overrides: { ...overrides,
    '/vehicles': route => route.fulfill({ status: 503, json: { error: 'Temporär nicht verfügbar' } }),
    '/charges/3': route => { writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 3 } }); },
  } });
  await page.goto(origin + '/#/finance');
  await page.getByRole('button', { name: 'Batterieservice bearbeiten', exact: true }).click();
  await expect(page.locator('#f_vehicle_id')).toBeVisible();
  await expect(page.locator('#f_vehicle_id')).toHaveValue('1');
  await expect(page.locator('#f_billing').locator('..')).toBeHidden();
  await page.locator('#f_description').fill('Batterieservice korrigiert');
  await save(page, writes);
  expect(writes[0]).toEqual({ person_id: 1, description: 'Batterieservice korrigiert', amount: 30, quantity: 1, charged_on: '2026-08-30', vehicle_id: 1 });
});

test('editing a vehicle never reprices or changes slider-managed status and reservation', async ({ page }) => {
  const writes = [];
  const vehicle = { ...overrides['/vehicles'][0], rate: 47, needs_power: true, notes: 'Bestehend', planner_symbol: 'PKW', reserved_until: '2026-09-09' };
  await mockUI(page, { overrides: { ...overrides, '/vehicles': [vehicle],
    '/vehicles/1': route => { writes.push(route.request().postDataJSON()); return route.fulfill({ json: vehicle }); },
  } });
  await page.goto(origin + '/#/vehicles/1');
  await page.getByRole('button', { name: 'Bearbeiten', exact: true }).click();
  await expect(page.locator('#f_notes')).toBeVisible();
  await expect(page.locator('#f_rate')).toHaveValue('47');
  await page.getByRole('button', { name: 'jährlich', exact: true }).click();
  await expect(page.locator('#f_rate')).toHaveValue('47');
  await save(page, writes);
  expect(writes[0]).toMatchObject({ rate: 47, billing_period: 'yearly', status: 'reserved', reserved_from: '2026-09-04', reserved_until: '2026-09-09', end_date: '2026-09-10', needs_power: true, notes: 'Bestehend', planner_symbol: 'PKW' });
});

test('agreement keeps its vehicle manager visible and submits added rows and closed note', async ({ page }) => {
  const writes = [];
  await mockUI(page, { overrides: { ...overrides,
    '/persons/1/agreements': route => { writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 5 } }); },
  } });
  await page.goto(origin + '/#/persons/1');
  await page.getByRole('button', { name: '+ Pauschale', exact: true }).click();
  await expect(page.getByText('Keine bestimmten Gefährte – die Pauschale deckt dann alle Gefährte der Person.', { exact: true })).toBeVisible();
  await page.locator('#f_amount').fill('120');
  await page.getByRole('button', { name: '+ Neues Gefährt', exact: true }).click();
  await page.getByRole('textbox', { name: 'Bezeichnung des Gefährts' }).fill('Anhänger');
  await page.getByRole('textbox', { name: 'Kennzeichen des Gefährts' }).fill('W-TEST');
  await page.getByText('Notiz hinzufügen', { exact: true }).click();
  await page.locator('#f_note').fill('Vereinbarung');
  await page.getByText('Notiz hinzufügen', { exact: true }).click();
  await save(page, writes);
  expect(writes[0]).toEqual({ amount: 120, period: 'monthly', start_date: '2026-09-10', end_date: null, note: 'Vereinbarung', vehicle_ids: [], new_vehicles: [{ category_id: 1, label: 'Anhänger', license_plate: 'W-TEST' }], edit_vehicles: [] });
});

test('tariff price coupling and hidden dimensions retain the existing contract', async ({ page }) => {
  const writes = [];
  await mockUI(page, { overrides: { ...overrides, '/categories/1': route => {
    writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 1 } });
  } } });
  await page.goto(origin + '/#/tariffs');
  await page.locator('.cfg-head').first().click();
  await expect(page.getByRole('spinbutton', { name: 'Standardlänge in Metern' })).toBeHidden();
  await page.locator('#cat-sync-1').focus(); await page.keyboard.press('Space');
  await page.getByLabel('Preis / Monat (€)', { exact: true }).fill('90');
  await expect(page.getByLabel('Preis / Jahr (€)', { exact: true })).toHaveValue('1080');
  await page.getByText('Standardmaße für den Planer', { exact: true }).click();
  await page.getByRole('spinbutton', { name: 'Standardlänge in Metern' }).fill('6.25');
  await page.getByText('Standardmaße für den Planer', { exact: true }).click();
  await page.getByRole('button', { name: 'Speichern', exact: true }).click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toEqual({ name: 'Wohnmobil', default_monthly_cost: 90, default_yearly_cost: 1080, rates_synced: true, default_length_m: 6.25, default_width_m: null, default_height_m: null, default_weight_t: null });
});

test('audit chips stay visible when filters close and clearing reopens the focus target', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides });
  await page.goto(origin + '/#/audit');
  const summary = page.locator('.audit-advanced summary');
  await summary.click();
  await page.getByRole('combobox', { name: 'Aktion filtern' }).selectOption('create');
  await summary.click();
  const clear = page.getByRole('button', { name: 'Filter Aktion: Erstellt entfernen', exact: true });
  await expect(clear).toBeVisible(); await clear.click();
  await expect(page.getByRole('combobox', { name: 'Aktion filtern' })).toBeFocused();
  await expect(page.getByRole('combobox', { name: 'Aktion filtern' })).toHaveValue('');
});

for (const lang of ['de', 'en']) {
  test(`portal progressively discloses forms and submits pickup in ${lang}`, async ({ page }) => {
    const writes = [];
    await mockUI(page, { overrides: { ...overrides, '/portal/requests': route => {
      writes.push(route.request().postDataJSON()); return route.fulfill({ json: { id: 1 } });
    } } });
    await page.goto(origin + '/#/portal/demo');
    if (lang === 'en') await page.getByRole('button', { name: 'Switch to English' }).click();
    await expect(page.locator('.portal-form').first()).toBeHidden();
    await page.locator('.portal-request summary').last().click();
    const form = page.locator('.portal-form').last();
    await form.locator('input[type=date]').fill('2026-09-15');
    await form.locator('input[type=text]').fill('  Vormittag  ');
    await form.getByRole('button').click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0]).toEqual({ kind: 'pickup', date: '2026-09-15', note: 'Vormittag' });
  });
}

for (const [width, theme] of [[1440, 'light'], [390, 'dark'], [320, 'light']]) {
  test(`compact dialog reflow, keyboard and accessibility ${width} ${theme}`, async ({ page }) => {
    test.setTimeout(90000);
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
    await mockUI(page, { role: 'admin', overrides });
    for (const route of ['persons', 'vehicles', 'finance']) {
      await openNew(page, route);
      const dialog = page.locator('#modal');
      await page.screenshot({ path: path.resolve(__dirname, '../../.impeccable/review/compact-workflows', `${route}-${width}-${theme}.png`) });
      expect(await dialog.evaluate(node => node.scrollWidth <= node.clientWidth), route + ' dialog overflow').toBe(true);
      expect(await page.locator('#modal-body').evaluate(node => node.scrollWidth <= node.clientWidth), route + ' body overflow').toBe(true);
      const result = await new AxeBuilder({ page }).include('#modal').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
      expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact)), route).toEqual([]);
      await page.keyboard.press('Escape'); await expect(dialog).toBeHidden();
    }
    await page.goto(origin + '/#/garages');
    await page.getByRole('button', { name: '+ Garage', exact: true }).click();
    await expect(page.locator('#modal')).not.toHaveClass(/modal-wide/);
    await expect(page.locator('#f_name')).toBeFocused();
  });
}
