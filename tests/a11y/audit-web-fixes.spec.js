const { test, expect } = require('@playwright/test');
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');
test.use({ serviceWorkers: 'block' });

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('parkrr_portal_lang', 'de'));
});

// WEB-01: one Idempotency-Key per "Rechnung bezahlen" dialog, reused on retry.
test('invoice payment retries with the same Idempotency-Key', async ({ page }) => {
  const keys = [];
  let calls = 0;
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/persons/1/pay-invoices': route => {
      keys.push(route.request().headers()['idempotency-key']);
      if (++calls === 1) return route.abort('connectionreset');
      return route.fulfill({ json: { unallocated: 0 } });
    },
  } });
  await page.goto(origin + '/#/invoices/1');
  await page.getByRole('button', { name: 'Bezahlen', exact: true }).click();
  const dlg = page.locator('#modal');
  await dlg.getByRole('button', { name: 'Speichern' }).click();
  await expect(dlg.locator('#modal-form-error')).toBeVisible();
  await dlg.getByRole('button', { name: 'Speichern' }).click();
  await expect(dlg).not.toBeVisible();
  expect(keys).toHaveLength(2);
  expect(keys[0]).toMatch(/^[0-9a-f-]{32,36}$/);
  expect(keys[1]).toBe(keys[0]);
});

// WEB-02: create requests carry a per-dialog key, edits (PUT) do not.
test('charge creation sends one Idempotency-Key per dialog, edits send none', async ({ page }) => {
  const posts = [];
  const puts = [];
  let calls = 0;
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/charges': route => {
      if (route.request().method() !== 'POST') return route.fulfill({ json: overrides['/charges'] });
      posts.push(route.request().headers()['idempotency-key']);
      if (++calls === 1) return route.abort('connectionreset');
      return route.fulfill({ json: { id: 99 } });
    },
    '/charges/1': route => { puts.push(route.request().headers()['idempotency-key']); return route.fulfill({ json: {} }); },
  } });
  await page.goto(origin + '/#/persons/1');
  await page.getByRole('button', { name: '+ Position' }).click();
  const dlg = page.locator('#modal');
  await dlg.locator('#f_description').fill('Innenreinigung');
  await dlg.locator('#f_amount').fill('45');
  await dlg.getByRole('button', { name: 'Speichern' }).click();
  await expect(dlg.locator('#modal-form-error')).toBeVisible();
  await dlg.getByRole('button', { name: 'Speichern' }).click();
  await expect(dlg).not.toBeVisible();
  expect(posts).toHaveLength(2);
  expect(posts[0]).toBeTruthy();
  expect(posts[1]).toBe(posts[0]);

  // A new dialog gets a new key.
  await page.getByRole('button', { name: '+ Position' }).click();
  await dlg.locator('#f_description').fill('Stromanschluss');
  await dlg.locator('#f_amount').fill('20');
  await dlg.getByRole('button', { name: 'Speichern' }).click();
  await expect(dlg).not.toBeVisible();
  expect(posts).toHaveLength(3);
  expect(posts[2]).not.toBe(posts[0]);

  await page.goto(origin + '/#/finance');
  await page.getByRole('button', { name: 'Innenreinigung bearbeiten' }).first().click();
  await dlg.getByRole('button', { name: 'Speichern' }).click();
  await expect(dlg).not.toBeVisible();
  expect(puts).toEqual([undefined]);
});

test('recurring charge creation sends an Idempotency-Key', async ({ page }) => {
  const keys = [];
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/persons/1/recurring': route => {
      if (route.request().method() !== 'POST') return route.fulfill({ json: [] });
      keys.push(route.request().headers()['idempotency-key']);
      return route.fulfill({ json: { id: 5 } });
    },
  } });
  await page.goto(origin + '/#/persons/1');
  await page.getByRole('button', { name: '+ Position' }).click();
  const dlg = page.locator('#modal');
  await dlg.getByRole('button', { name: 'monatlich' }).click();
  await dlg.locator('#f_description').fill('Stromanschluss');
  await dlg.locator('#f_amount').fill('20');
  await dlg.getByRole('button', { name: 'Speichern' }).click();
  await expect(dlg).not.toBeVisible();
  expect(keys).toHaveLength(1);
  expect(keys[0]).toBeTruthy();
});

// WEB-05: a 401 on a form save logs out once and closes the dialog.
test('session expiry during a form save returns to login once', async ({ page }) => {
  const requests = await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/persons/1/payments': route => route.request().method() === 'POST'
      ? route.fulfill({ status: 401, json: { error: 'not authenticated' } })
      : route.fulfill({ json: [] }),
    '/auth/logout': route => route.fulfill({ status: 401, json: { error: 'not authenticated' } }),
  } });
  await page.goto(origin + '/#/persons/1');
  await page.getByRole('button', { name: '+ Zahlung' }).click();
  const dlg = page.locator('#modal');
  await dlg.getByRole('button', { name: 'Speichern' }).click();
  await expect(page.locator('#login-view')).toBeVisible();
  await expect(page.locator('#app-view')).toBeHidden();
  await expect(dlg).not.toBeVisible();
  await expect(page.locator('#toast')).toContainText('Sitzung abgelaufen');
  expect(requests.filter(r => r.path === '/auth/logout')).toHaveLength(1);
});

test('a failed login attempt does not trigger the session-expiry logout', async ({ page }) => {
  const requests = await mockUI(page, { loggedIn: false, overrides: { ...overrides,
    '/auth/login': route => route.fulfill({ status: 401, json: { error: 'Benutzername oder Passwort ist falsch' } }),
  } });
  await page.goto(origin + '/');
  await page.locator('#login-username').fill('demo');
  await page.locator('#login-password').fill('falsch');
  await page.locator('#login-form button[type="submit"]').click();
  await expect(page.locator('#login-error')).toContainText('Benutzername oder Passwort ist falsch');
  expect(requests.filter(r => r.path === '/auth/logout')).toHaveLength(0);
});

// ---- planner autosave (WEB-03 / WEB-04) ----
// The hall opens in "Stellplätze" (manage) mode, where vehicles are selectable.
async function openPlanner(page) {
  await page.goto(origin + '/#/hall/1');
  await expect(page.locator('.gp-block.veh[data-id="1"]')).toBeVisible();
}
async function selectSpot(page) {
  await page.locator('.gp-block.veh[data-id="1"]').click();
  await expect(page.locator('.gp-segd')).toBeVisible();
}
const setStatus = (page, label) => page.locator('.gp-segd button', { hasText: label }).click();
const hallPuts = (requests) => requests.filter(r => r.path === '/halls/1' && r.method === 'PUT').length;

test('planner autosave stops on 401 and hands over to login', async ({ page }) => {
  const requests = await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/halls/1': route => route.fulfill({ status: 401, json: { error: 'not authenticated' } }),
  } });
  await openPlanner(page);
  await selectSpot(page);
  await setStatus(page, 'Reserviert');
  await expect(page.locator('#login-view')).toBeVisible();
  await page.waitForTimeout(2500);
  expect(hallPuts(requests)).toBe(1);
});

test('planner autosave backs off exponentially on 5xx', async ({ page }) => {
  const requests = await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/halls/1': route => route.fulfill({ status: 503, json: { error: 'unavailable' } }),
  } });
  await openPlanner(page);
  await selectSpot(page);
  await setStatus(page, 'Reserviert');
  await expect.poll(() => hallPuts(requests)).toBe(1);
  // Fixed 800 ms retries would have fired about five times by now; backoff waits 0.8, 1.6, 3.2 s.
  await page.waitForTimeout(4000);
  expect(hallPuts(requests)).toBeLessThanOrEqual(3);
  await expect(page.locator('.gp-tbtn', { hasText: 'Speichern' })).toBeEnabled();
});

test('leaving the planner flushes a pending edit once and then stops', async ({ page }) => {
  const requests = await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/halls/1': route => route.fulfill({ status: 503, json: { error: 'unavailable' } }),
  } });
  await openPlanner(page);
  await selectSpot(page);
  await setStatus(page, 'Reserviert');
  await page.evaluate(() => { location.hash = '#/dashboard'; });
  await expect.poll(() => hallPuts(requests)).toBe(1);
  await page.waitForTimeout(3000);
  expect(hallPuts(requests)).toBe(1);
});

test('a spot edit made while its PUT is in flight is not dropped', async ({ page }) => {
  const spotBodies = [];
  let release;
  const held = new Promise(resolve => { release = resolve; });
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/halls/1': route => route.fulfill({ json: {} }),
    '/spots/1': async route => {
      spotBodies.push(route.request().postDataJSON());
      if (spotBodies.length === 1) await held;
      return route.fulfill({ json: {} });
    },
  } });
  await openPlanner(page);
  await selectSpot(page);
  await setStatus(page, 'Reserviert');
  await expect.poll(() => spotBodies.length).toBe(1);
  await setStatus(page, 'Ein/Aus');
  release(); // the first PUT finishes before the next autosave runs
  await expect.poll(() => spotBodies.length, { timeout: 5000 }).toBe(2);
  expect(spotBodies[0].geometry.status).toBe('resv');
  expect(spotBodies[1].geometry.status).toBe('move');
});
