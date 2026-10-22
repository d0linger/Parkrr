const { test, expect } = require('@playwright/test');

// This scenario is deliberately unavailable on developer/production instances.
const LIVE_READY = process.env.PARKRR_E2E_ISOLATED === '1' &&
  ['http://localhost:18099', 'http://localhost:18101'].includes(process.env.PARKRR_BASE_URL);
const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';
// CI setzt PARKRR_E2E_REQUIRE_LIVE: dort ist ein Überspringen ein Fehler, sonst
// läuft der Test nie und meldet trotzdem Grün.
if (!LIVE_READY && process.env.PARKRR_E2E_REQUIRE_LIVE === '1') {
  throw new Error('PARKRR_E2E_REQUIRE_LIVE=1, but the live scenario needs PARKRR_E2E_ISOLATED=1 and PARKRR_BASE_URL localhost:18099 or :18101');
}
test.skip(!LIVE_READY, 'Requires an explicitly disposable comprehensive-test app on localhost:18099 or localhost:18101');

test('live forecast uses real read-only responses while planning stays inside the chart', async ({ page, context }, testInfo) => {
  const errors = [], serverErrors = [], chartRequests = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('response', response => {
    if (response.status() >= 500) serverErrors.push({ status: response.status(), url: response.url() });
  });
  await page.goto('/');
  await page.locator('#login-username').fill(USER);
  await page.locator('#login-password').fill(PASS);
  const overviewResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/api/overview');
  await page.locator('#login-form button[type="submit"]').click();
  await expect(page.locator('#app-view')).toBeVisible();
  await overviewResponse;
  const csrf = (await context.cookies()).find(cookie => cookie.name === 'parkrr_csrf');
  expect(csrf, 'Expected CSRF cookie after login').toBeDefined();
  const headers = { 'X-CSRF-Token': csrf.value };
  let personId, chargeId;
  try {
    // A fresh disposable database intentionally renders the empty dashboard.
    // Own this test's source records so the chart never depends on another suite.
    const personResponse = await page.request.post('/api/persons', {
      headers,
      data: { first_name: 'Forecast', last_name: 'Live-' + Date.now() },
    });
    expect(personResponse.status()).toBe(201);
    personId = (await personResponse.json()).id;
    const chargeResponse = await page.request.post('/api/charges', {
      headers,
      data: { person_id: personId, description: 'Isolated forecast fixture', amount: 123.45, quantity: 1 },
    });
    expect(chargeResponse.status()).toBe(201);
    chargeId = (await chargeResponse.json()).id;
    const seededOverview = page.waitForResponse(response => new URL(response.url()).pathname === '/api/overview');
    await page.reload();
    const overview = await (await seededOverview).json();
    const year = overview.current_year;
    expect(Number.isInteger(year)).toBe(true);
    const dashboardYear = page.getByRole('combobox', { name: 'Dashboard-Jahr' });
    await expect(dashboardYear).toHaveValue(String(year));
    const chart = page.locator('.revenue-chart');
    await expect(chart.getByLabel('Prognose anzeigen', { exact: true })).not.toBeChecked();
    const originalCards = await page.locator('.dash-money, .dash-hero, .dash-counts').allTextContents();
    page.on('request', request => {
      if (new URL(request.url()).pathname === '/api/overview/forecast') {
        chartRequests.push({ method: request.method(), url: request.url() });
      }
    });

    const responseFor = selectedYear => page.waitForResponse(response => {
      const url = new URL(response.url());
      return url.pathname === '/api/overview/forecast' && url.searchParams.get('year') === String(selectedYear);
    });
    const currentResponse = responseFor(year);
    await chart.getByLabel('Prognose anzeigen', { exact: true }).check();
    const current = await currentResponse;
    expect(current.status()).toBe(200);
    const currentData = await current.json();
    expect(currentData.year).toBe(year);
    for (const key of ['actual_by_month', 'forecast_by_month', 'reservations_by_month']) {
      expect(currentData[key]).toHaveLength(12);
      expect(currentData[key].every(Number.isFinite)).toBe(true);
    }
    await expect(chart.locator('.revenue-totals')).toBeVisible();
    await expect(chart.getByRole('alert')).toBeHidden();
    await chart.getByRole('button', { name: 'Jahre', exact: true }).click();
    await expect(chart.getByRole('heading')).toHaveText('Umsatz im Jahresvergleich · ' + year);
    await chart.getByRole('button', { name: 'Monate', exact: true }).click();
    await expect(chart.getByRole('heading')).toHaveText('Umsatz pro Monat · ' + year);

    const nextResponse = responseFor(year + 1);
    await chart.getByRole('combobox', { name: 'Planungsjahr der Umsatzprognose' }).selectOption(String(year + 1));
    const next = await nextResponse;
    expect(next.status()).toBe(200);
    const nextData = await next.json();
    expect(nextData.year).toBe(year + 1);
    expect(nextData.previous_by_month).toBeNull();
    await expect(chart.locator('.revenue-totals')).toContainText('Aufgelaufen ' + (year + 1));
    await expect(chart.getByRole('heading')).toHaveText('Umsatz pro Monat · ' + (year + 1));
    await expect(dashboardYear).toHaveValue(String(year));
    await expect(dashboardYear.locator(`option[value="${year + 1}"]`)).toHaveCount(0);
    expect(await page.locator('.dash-money, .dash-hero, .dash-counts').allTextContents()).toEqual(originalCards);

    await chart.getByRole('button', { name: 'Jahre', exact: true }).click();
    await expect(chart.locator('.revenue-axis').last()).toHaveText(String(year + 1));
    await chart.getByLabel('Prognose anzeigen', { exact: true }).uncheck();
    await expect(chart.getByRole('heading')).toHaveText('Umsatz im Jahresvergleich · ' + year);
    await expect(chart.locator('.revenue-axis').last()).toHaveText(String(year));
    await expect(chart.locator('.revenue-totals')).toHaveCount(0);
    await chart.getByRole('button', { name: 'Monate', exact: true }).click();
    await expect(chart.getByRole('heading')).toHaveText('Umsatz pro Monat · ' + year);
    await expect(chart.getByRole('alert')).toBeHidden();
    expect(chartRequests.some(request => new URL(request.url).searchParams.get('year') === String(year))).toBe(true);
    expect(chartRequests.some(request => new URL(request.url).searchParams.get('year') === String(year + 1))).toBe(true);
    expect(chartRequests.every(request => request.method === 'GET')).toBe(true);
    expect(errors).toEqual([]);
    expect(serverErrors).toEqual([]);
    await testInfo.attach('synthetic-live-forecast-responses', {
      body: JSON.stringify({ current: currentData, next: nextData, chartRequests, errors, serverErrors }, null, 2),
      contentType: 'application/json',
    });
  } finally {
    // Best-effort teardown must not replace the original assertion failure.
    if (chargeId) await page.request.delete('/api/charges/' + chargeId, { headers }).catch(() => {});
    if (personId) await page.request.delete('/api/persons/' + personId, { headers }).catch(() => {});
  }
});
