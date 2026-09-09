// @ts-check
// Hundert 54 + 55 + 57 (UI-Haelfte): Bulk-Aktionen fuer Gefaehrte, die
// Kalenderansicht und die Anhaenge-Karte — alle drei ueber die echte Oberflaeche.
const { test, expect } = require('@playwright/test');
test.setTimeout(90000);

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

async function login(page) {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
}

async function seedVehicles(page, n) {
  return page.evaluate(async (count) => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const hdr = { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf };
    const person = await (await fetch('/api/persons', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({ first_name: 'Bulk', last_name: 'SeedB54' }) })).json();
    // Eigene Kategorie sicherstellen statt eine fremde vorauszusetzen: mit zwei
    // parallelen Workern haengt es sonst von der Dateireihenfolge ab, ob schon eine
    // existiert — cats war dann leer, cat undefined, und der Test fiel mit
    // "Cannot read properties of undefined (reading 'id')" beim SEEDEN um, nicht
    // an dem, was er prueft.
    const cats = await (await fetch('/api/categories', { credentials: 'same-origin' })).json();
    let cat = Array.isArray(cats) ? (cats.find((c) => !c.archived) || cats[0]) : undefined;
    if (!cat || !cat.id) {
      cat = await (await fetch('/api/categories', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({ name: 'BulkKat-SeedB54' }) })).json();
    }
    const ids = [];
    for (let i = 0; i < count; i++) {
      const v = await (await fetch('/api/vehicles', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({ person_id: person.id, category_id: cat.id, status: 'stored', label: 'BulkAuto-' + Date.now() + '-' + i, billing_period: 'monthly', start_date: new Date().toISOString().slice(0, 10) }) })).json();
      ids.push(v.id);
    }
    return { pid: person.id, ids };
  }, n);
}

async function cleanup(page) {
  await page.evaluate(async () => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const list = await (await fetch('/api/persons', { credentials: 'same-origin' })).json();
    for (const p of list.filter((x) => x.last_name === 'SeedB54')) {
      await fetch('/api/persons/' + p.id, { method: 'DELETE', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf } });
    }
  });
}

test('Bulk: zwei Gefaehrte gemeinsam als abgeholt markieren', async ({ page }) => {
  await login(page);
  const seed = await seedVehicles(page, 2);
  try {
    await page.goto('/#/vehicles');
    await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
    await page.getByRole('button', { name: 'Mehrfachauswahl' }).click();
    await page.waitForSelector('.bulk-cb', { timeout: 15000 });
    // Unsere zwei Karten anhaken (ueber die Suche eingrenzen).
    await page.fill('.search', 'BulkAuto');
    await page.waitForTimeout(400);
    const boxes = page.locator('.bulk-cb');
    await expect(boxes.first()).toBeVisible();
    const n = await boxes.count();
    for (let i = 0; i < n; i++) await boxes.nth(i).check();
    await page.locator('.bulk-bar').getByRole('button', { name: 'Als abgeholt markieren' }).click();
    await page.locator('#confirm-ok').click();
    await expect(page.locator('.toast, [class*=toast]').last()).toContainText('erledigt', { timeout: 20000 });
    // Serverseitige Wahrheit: beide collected mit Enddatum.
    const states = await page.evaluate(async (ids) => {
      const list = await (await fetch('/api/vehicles', { credentials: 'same-origin' })).json();
      return ids.map((id) => { const v = list.find((x) => x.id === id); return v && { s: v.status, e: v.end_date }; });
    }, seed.ids);
    for (const st of states) {
      expect(st.s).toBe('collected');
      expect(st.e).toBeTruthy();
    }
  } finally { await cleanup(page); }
});

test('Bulk: der Zaehler zaehlt schon beim ERSTEN Anhaken', async ({ page }) => {
  await login(page);
  const seed = await seedVehicles(page, 2);
  try {
    await page.goto('/#/vehicles');
    await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
    // BEWUSST ohne Suche/Filter dazwischen: genau die erste Darstellung nach dem
    // Einschalten war kaputt. Die Leiste entstand erst NACH mountList, dessen
    // refresh() synchron laeuft — jede Checkbox fing damit `undefined` statt der
    // Leiste ein, und der Zaehler blieb bei 0. Der aeltere Bulk-Test hier fiel nicht
    // darauf herein, weil er vor dem Anhaken die Suche benutzt: die loest ein
    // zweites refresh() aus, bei dem die Leiste dann steht.
    await page.getByRole('button', { name: 'Mehrfachauswahl' }).click();
    await page.waitForSelector('.bulk-cb', { timeout: 15000 });
    await page.locator('.bulk-cb').first().check();
    await expect(page.locator('.bulk-bar b')).toHaveText('1', { timeout: 5000 });
    await page.locator('.bulk-cb').nth(1).check();
    await expect(page.locator('.bulk-bar b')).toHaveText('2', { timeout: 5000 });
  } finally { await cleanup(page); }
});

test('Kalender: Abholung des Monats erscheint als Eintrag', async ({ page }) => {
  await login(page);
  const seed = await seedVehicles(page, 1);
  try {
    // Enddatum in diesem Monat setzen (Status abgeholt).
    await page.evaluate(async (id) => {
      const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
      await fetch('/api/vehicles/' + id + '/status', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify({ status: 'collected' }) });
    }, seed.ids[0]);
    await page.goto('/#/calendar');
    await page.waitForSelector('.cal-grid', { timeout: 30000 });
    await expect(page.locator('.cal-ev.cal-pickup', { hasText: 'BulkAuto' }).first()).toBeVisible({ timeout: 15000 });
  } finally { await cleanup(page); }
});

test('Anhaenge: PDF hochladen, gelistet, Download erzwungen', async ({ page }) => {
  await login(page);
  const seed = await seedVehicles(page, 1);
  try {
    await page.goto('/#/person/' + seed.pid);
    await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
    await expect(page.getByRole('button', { name: '+ Anhang' })).toBeVisible({ timeout: 15000 });
    await page.evaluate(() => {
      const pdf = new Blob(['%PDF-1.4\n%%EOF'], { type: 'application/pdf' });
      const file = new File([pdf], 'vertrag.pdf', { type: 'application/pdf' });
      const dt = new DataTransfer(); dt.items.add(file);
      const input = document.querySelector('input[type=file][accept="application/pdf,image/jpeg,image/png"]');
      input.files = dt.files;
      input.dispatchEvent(new Event('change', { bubbles: true }));
    });
    await expect(page.locator('.att-row', { hasText: 'vertrag.pdf' })).toBeVisible({ timeout: 20000 });
    const head = await page.evaluate(async () => {
      const a = document.querySelector('.att-name');
      const r = await fetch(a.getAttribute('href'), { credentials: 'same-origin' });
      return { cd: r.headers.get('content-disposition'), ct: r.headers.get('content-type') };
    });
    expect(head.cd).toContain('attachment');
    expect(head.ct).toBe('application/pdf');
  } finally { await cleanup(page); }
});
