// @ts-check
// E2E für die Anmeldewege (Hundert 93): Passwort (falsch/richtig), 2FA-Einrichtung
// samt Login mit echtem TOTP-Code und Recovery-Code, Passkey über den virtuellen
// WebAuthn-Authenticator von Chromium. Bisher war der komplette Anmeldepfad nur
// serverseitig getestet — der Browseranteil (Masken, Codefelder, Redirects) nie.
const { test, expect } = require('@playwright/test');
const crypto = require('crypto');
test.setTimeout(90000);

const ADMIN = process.env.PARKRR_E2E_USER || 'admin';
const APASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

// RFC-6238-TOTP in 15 Zeilen Node — der Test rechnet den Code selbst aus, statt
// einem gemockten Validator zu glauben.
function totp(secretB32, at = Date.now()) {
  const alph = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const c of secretB32.replace(/=+$/, '').toUpperCase()) {
    const v = alph.indexOf(c);
    if (v < 0) continue;
    bits += v.toString(2).padStart(5, '0');
  }
  const bytes = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(at / 1000 / 30)));
  const h = crypto.createHmac('sha1', bytes).update(counter).digest();
  const off = h[h.length - 1] & 0x0f;
  const code = ((h.readUInt32BE(off) & 0x7fffffff) % 1e6).toString().padStart(6, '0');
  return code;
}

async function loginAs(page, user, pass) {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', user);
  await page.fill('#login-password', pass);
  await page.click('#login-form button[type="submit"]');
}

// Admin-API-Aufruf im Seitenkontext (trägt Sitzung + CSRF).
async function apiCall(page, method, path, body) {
  return page.evaluate(async ({ method, path, body }) => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const r = await fetch('/api' + path, {
      method, credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    let data = null; try { data = await r.json(); } catch (e) { /* leer */ }
    return { status: r.status, data };
  }, { method, path, body });
}

test('Login: falsches Passwort bleibt draußen, richtiges kommt hinein', async ({ page }) => {
  await loginAs(page, ADMIN, 'definitiv-falsch');
  // Die Maske bleibt stehen und meldet den Fehler; die App öffnet sich nicht.
  await page.waitForTimeout(800);
  await expect(page.locator('#app-view')).toBeHidden();
  await loginAs(page, ADMIN, APASS);
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
});

test('2FA: Einrichtung, Login mit TOTP-Code und mit Recovery-Code', async ({ page }) => {
  const uname = 'e2e2fa' + Date.now();
  const upass = 'e2e-zwei-faktor-passwort';

  // Benutzer als Admin anlegen.
  await loginAs(page, ADMIN, APASS);
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
  const created = await apiCall(page, 'POST', '/users', { username: uname, email: uname + '@example.com', role: 'editor', password: upass });
  expect(created.status, JSON.stringify(created.data)).toBe(201);
  await apiCall(page, 'POST', '/auth/logout');

  // Als neuer Benutzer: 2FA über die API des eigenen Kontos einrichten (die
  // Step-up-Regel erlaubt das direkt nach dem Login ohne erneutes Passwort).
  await loginAs(page, uname, upass);
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
  const setup = await apiCall(page, 'POST', '/auth/2fa/setup');
  expect(setup.status, JSON.stringify(setup.data)).toBe(200);
  const secret = setup.data.secret;
  const enable = await apiCall(page, 'POST', '/auth/2fa/enable', { code: totp(secret) });
  expect(enable.status, JSON.stringify(enable.data)).toBe(200);
  const backupCodes = enable.data.backup_codes || [];
  expect(backupCodes.length).toBeGreaterThan(0);
  await apiCall(page, 'POST', '/auth/logout');

  // Login-Maske: Passwort → 2FA-Schritt erscheint → echter TOTP-Code über die
  // sechs Ziffernfelder.
  await loginAs(page, uname, upass);
  await page.waitForSelector('#login-totp-wrap:not([hidden])', { timeout: 15000 });
  const code = totp(secret);
  for (let i = 0; i < 6; i++) await page.fill('#login-otp-' + i, code[i]);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
  await apiCall(page, 'POST', '/auth/logout');

  // Und derselbe Weg mit einem Recovery-Code (Modus wechseln).
  await loginAs(page, uname, upass);
  await page.waitForSelector('#login-totp-wrap:not([hidden])', { timeout: 15000 });
  await page.click('#login-backup-toggle');
  await page.fill('#login-backup', backupCodes[0]);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  // Aufräumen als Admin.
  await apiCall(page, 'POST', '/auth/logout');
  await loginAs(page, ADMIN, APASS);
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
  const list = await apiCall(page, 'GET', '/users');
  const hit = (list.data || []).find((u) => u.username === uname);
  if (hit) await apiCall(page, 'DELETE', '/users/' + hit.id);
});

test('Passkey: registrieren, abmelden, per Passkey anmelden', async ({ page }) => {
  const caps = await page.request.get('/api/auth/capabilities');
  const capsJson = await caps.json();
  test.skip(!capsJson.passkeys, 'WebAuthn ist auf dieser Instanz nicht konfiguriert (PARKRR_WEBAUTHN_RP_ID)');

  // Virtueller Authenticator (CDP): resident key + user verification, damit auch
  // der benutzerlose Passkey-Login (discoverable credential) funktioniert.
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });

  const uname = 'e2epk' + Date.now();
  const upass = 'e2e-passkey-passwort';
  await loginAs(page, ADMIN, APASS);
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
  const created = await apiCall(page, 'POST', '/users', { username: uname, email: uname + '@example.com', role: 'editor', password: upass });
  expect(created.status).toBe(201);
  await apiCall(page, 'POST', '/auth/logout');

  await loginAs(page, uname, upass);
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
  // Registrierung über die Einstellungen (der echte UI-Weg).
  await page.goto('/#/settings');
  await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
  const addBtn = page.getByRole('button', { name: /\+ Passkey/ });
  await expect(addBtn.first()).toBeVisible({ timeout: 15000 });
  await addBtn.first().click();
  // Erfolg: die Liste zeigt einen Eintrag (virtueller Authenticator bestätigt automatisch).
  await expect(page.locator('#page')).toContainText(/Passkey/, { timeout: 15000 });
  await page.waitForTimeout(800);
  const pkList = await apiCall(page, 'GET', '/passkeys');
  expect((pkList.data || []).length, 'Passkey wurde nicht registriert').toBeGreaterThan(0);
  await apiCall(page, 'POST', '/auth/logout');

  // Login NUR per Passkey.
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  const pkBtn = page.locator('#passkey-login');
  await expect(pkBtn).toBeVisible();
  await pkBtn.click();
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  // Aufräumen.
  await apiCall(page, 'POST', '/auth/logout');
  await loginAs(page, ADMIN, APASS);
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
  const list = await apiCall(page, 'GET', '/users');
  const hit = (list.data || []).find((u) => u.username === uname);
  if (hit) await apiCall(page, 'DELETE', '/users/' + hit.id);
});
