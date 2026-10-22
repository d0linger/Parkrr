const { test, expect } = require('@playwright/test');

const isolated = process.env.PARKRR_E2E_ISOLATED === '1' &&
  ['http://localhost:18099', 'http://localhost:18101'].includes(process.env.PARKRR_BASE_URL);
if (!isolated && process.env.PARKRR_E2E_REQUIRE_LIVE === '1') {
  throw new Error('Label print regression requires an explicitly disposable E2E server.');
}
test.skip(!isolated, 'Requires an explicitly disposable E2E server.');

test('A4 label sheets print ten labels per page without blank pages', async ({ page, context }, testInfo) => {
  test.setTimeout(90000);
  await page.goto('/');
  await page.locator('#login-username').fill(process.env.PARKRR_E2E_USER || 'admin');
  await page.locator('#login-password').fill(process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password');
  await page.locator('#login-form button[type="submit"]').click();
  await expect(page.locator('#app-view')).toBeVisible();
  const csrf = (await context.cookies()).find(cookie => cookie.name === 'parkrr_csrf');
  expect(csrf).toBeDefined();
  const headers = { 'X-CSRF-Token': csrf.value };
  const post = async (path, data) => {
    const response = await page.request.post('/api' + path, { headers, data });
    expect(response.status(), path + ': ' + await response.text()).toBe(201);
    return response.json();
  };
  const tag = Date.now();
  let person, category, garage;
  try {
    person = await post('/persons', { first_name: 'Label', last_name: 'Print-' + tag });
    category = await post('/categories', { name: 'Print-' + tag, default_monthly_cost: 20, default_yearly_cost: 200 });
    garage = await post('/garages', { name: 'Print-' + tag });
    const hall = await post('/garages/' + garage.id + '/halls', { name: 'Print', geometry: {} });
    for (let i = 1; i <= 30; i++) {
      const spot = await post('/halls/' + hall.id + '/spots', { label: 'P-' + String(i).padStart(2, '0'), geometry: { x: i * 5, y: 0, w: 4, h: 2 } });
      const vehicle = await post('/vehicles', { person_id: person.id, category_id: category.id,
        label: 'Print ' + i, start_date: '2026-01-01', billing_period: 'monthly', status: 'stored' });
      const assigned = await page.request.put('/api/spots/' + spot.id + '/vehicle', { headers, data: { vehicle_id: vehicle.id } });
      expect(assigned.ok(), await assigned.text()).toBe(true);
      if (![10, 11, 20, 30].includes(i)) continue;
      await page.goto('/api/halls/' + hall.id + '/labels');
      await expect(page.locator('.label')).toHaveCount(i);
      await page.emulateMedia({ media: 'print' });
      const pdf = await page.pdf({ preferCSSPageSize: true, printBackground: true });
      // Chromium emits an explicit /Type /Page dictionary for each PDF page.
      const pages = (pdf.toString('latin1').match(/\/Type\s*\/Page\b/g) || []).length;
      expect(pages, i + ' labels must have no extra or blank pages').toBe(Math.ceil(i / 10));
      await testInfo.attach('labels-' + i + '.pdf', { body: pdf, contentType: 'application/pdf' });
    }
  } finally {
    for (const [path, record] of [['/persons/', person], ['/garages/', garage], ['/categories/', category]]) {
      if (!record) continue;
      const response = await page.request.delete('/api' + path + record.id, { headers });
      expect(response.ok(), path + ': ' + await response.text()).toBe(true);
    }
  }
});
