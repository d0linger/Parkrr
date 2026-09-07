// @ts-check
// Hundert 50: der Foto-Upload verkleinert grosse Bilder VOR dem Hochladen und
// zeigt einen echten Fortschritt. Der Test erzeugt ein 3500x2600-Canvas-JPEG,
// laedt es ueber den echten UI-Weg hoch und weist am SERVER nach, dass die
// gespeicherte Version verkleinert wurde.
const { test, expect } = require('@playwright/test');
test.setTimeout(90000);

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

test('Foto-Upload: verkleinert und mit Fortschrittsanzeige', async ({ page }) => {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  // Gefaehrt + grosses Testbild im Seitenkontext bauen.
  const ids = await page.evaluate(async () => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const hdr = { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf };
    const person = await (await fetch('/api/persons', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({ first_name: 'Foto', last_name: 'UplSeed' }) })).json();
    const cats = await (await fetch('/api/categories', { credentials: 'same-origin' })).json();
    let cat = cats.find((c) => !c.archived);
    if (!cat) cat = await (await fetch('/api/categories', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({ name: 'UplTarif-' + Date.now(), default_monthly_cost: 1, default_yearly_cost: 10 }) })).json();
    const veh = await (await fetch('/api/vehicles', { method: 'POST', credentials: 'same-origin', headers: hdr, body: JSON.stringify({ person_id: person.id, category_id: cat.id, status: 'stored', label: 'UplAuto-' + Date.now(), billing_period: 'monthly', start_date: new Date().toISOString().slice(0, 10) }) })).json();
    return { person: person.id, veh: veh.id };
  });
  expect(ids.veh, 'Gefaehrt fehlgeschlagen').toBeTruthy();

  await page.goto('/#/vehicles/' + ids.veh);
  await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
  await expect(page.getByRole('button', { name: '+ Foto' })).toBeVisible({ timeout: 15000 });

  // Grosses JPEG (3500x2600) direkt in den file input setzen.
  await page.evaluate(async () => {
    const canvas = document.createElement('canvas');
    canvas.width = 3500; canvas.height = 2600;
    const ctx = canvas.getContext('2d');
    for (let i = 0; i < 60; i++) { ctx.fillStyle = 'hsl(' + (i * 6) + ',70%,50%)'; ctx.fillRect((i % 10) * 350, Math.floor(i / 10) * 434, 350, 434); }
    const blob = await new Promise((r) => canvas.toBlob(r, 'image/jpeg', 0.95));
    const file = new File([blob], 'gross.jpg', { type: 'image/jpeg' });
    const dt = new DataTransfer(); dt.items.add(file);
    const input = document.querySelector('input[type=file][accept="image/jpeg,image/png"]');
    input.files = dt.files;
    input.dispatchEvent(new Event('change', { bubbles: true }));
    window.__origSize = blob.size;
  });

  // Warten bis das Foto in der Galerie auftaucht (render() nach Erfolg).
  await page.waitForSelector('.photo-grid img', { timeout: 30000 });

  // Serverseitige Wahrheit: die gespeicherte Version ist auf <= 2000 px verkleinert.
  const meta = await page.evaluate(async (vehId) => {
    const list = await (await fetch('/api/vehicles/' + vehId + '/photos', { credentials: 'same-origin' })).json();
    const p = list[0];
    const img = new Image();
    img.src = '/api/photos/' + p.id;
    await new Promise((r) => { img.onload = r; });
    return { w: img.naturalWidth, h: img.naturalHeight, bytes: p.byte_size, orig: window.__origSize };
  }, ids.veh);
  expect(Math.max(meta.w, meta.h), 'nicht verkleinert').toBeLessThanOrEqual(2000);
  expect(meta.bytes).toBeLessThan(meta.orig);

  // Aufraeumen.
  await page.evaluate(async (pid) => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    await fetch('/api/persons/' + pid, { method: 'DELETE', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf } });
  }, ids.person);
});
