// Reproducible, offline visual comparison. No running app or database is used.
const fs = require('node:fs/promises');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const assert = require('node:assert/strict');
const { pathToFileURL } = require('node:url');
const { chromium, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

const root = path.resolve(__dirname, '../..');
const output = path.join(root, '.impeccable/review/before-after');
const fixedTime = '2026-09-10T12:00:00Z';
const revisions = { original: '9756ae2', previous: '539fb45', current: 'bc5c86e' };
const captures = [
  { id: 'light-1440', width: 1440, height: 900, theme: 'light', label: 'Desktop · 1440 px · Hell' },
  { id: 'dark-390', width: 390, height: 900, theme: 'dark', label: 'Mobil · 390 px · Dunkel' },
];
const surfaces = [
  ['dashboard', 'Übersicht', 'dashboard', 'Bewährte Geld-/Bestandsgruppen und Warnungen beibehalten.'],
  ['persons', 'Personen', 'persons', 'Suche und Sortierung als gemeinsame Gruppe.'],
  ['persons-1', 'Personendetail', 'persons/1', 'Kompaktere Abschnittsabstände; zugehörige Dialoge separat auswählbar.'],
  ['vehicles', 'Gefährte', 'vehicles', 'Gemeinsame Such-/Sortierzeile; direkte Status- und Zahlungsschalter bleiben erhalten.'],
  ['vehicles-1', 'Gefährtdetail', 'vehicles/1', 'Bestehende Übersicht beibehalten; Formularänderungen unter „Neues Gefährt“.'],
  ['finance', 'Zusatzkosten', 'finance', 'Such-/Sortiergruppe; Erfassungsdialog separat auswählbar.'],
  ['tariffs', 'Tarife & Dienste', 'tariffs', 'Bestehende aufklappbare Karten beibehalten; Standardmaße im geöffneten Tarif vergleichen.'],
  ['users', 'Benutzer', 'users', 'Such-/Sortiergruppe; Passwortänderung im Bearbeitungsdialog aufklappbar.'],
  ['billing', 'Rechnungs-Einstellungen', 'billing', 'Nummerierung und Zahlungsangaben paarweise nebeneinander.'],
  ['invoices-1', 'Rechnung', 'invoices/1', 'Dokument-/Aktionsstruktur und scrollbare Positionstabelle beibehalten.'],
  ['backup', 'Backup', 'backup', 'Bewährte Status-, Zeitplan- und Wiederherstellungsbereiche beibehalten.'],
  ['audit', 'Audit-Log', 'audit', 'Suche primär; Aktion, Objekt und Zeitraum erst bei Bedarf sichtbar.'],
  ['settings', 'Einstellungen', 'settings', 'Kompaktere Konto-/Sitzungszeilen, gleiche Sicherheitsfunktionen.'],
  ['calendar', 'Kalender', 'calendar', 'Monatsnavigation und Ansichtswechsel in einem gemeinsamen Kopf.'],
  ['garages', 'Garagen', 'garages', 'Gleichwertige Standortkarten auf großen Bildschirmen nebeneinander.'],
  ['garage-1', 'Hallen', 'garage/1', 'Hallenkarten auf großen Bildschirmen nebeneinander.'],
  ['hall-1', 'Planer', 'hall/1', 'Werkzeugleistenabstände vereinheitlicht; Arbeitsfläche und Platzierungsregeln beibehalten.'],
  ['login', 'Anmeldung', '', 'Bereits kompakte Anmeldung mit Hilfe unverändert beibehalten.'],
  ['portal', 'Kundenportal', 'portal/demo', 'Anliegen aufklappbar, keine gestreckten Desktop-Zeilen.'],
].map(([id, label, route, note]) => ({ id, label, route, note, kind: 'page' }));
const click = (name) => page => page.getByRole('button', { name, exact: true }).click();
surfaces.push(
  { id: 'form-person', label: 'Neue Person', route: 'persons', note: 'Name und Kontakt paarweise, Adresse/Notizen standardmäßig eingeklappt.', open: click('+ Neu') },
  { id: 'form-vehicle', label: 'Neues Gefährt', route: 'vehicles', note: 'Stammdaten, Datum und Preis gruppiert; zusätzliche Planerangaben aufklappbar.', open: click('+ Neu') },
  { id: 'form-charge', label: 'Neue Zusatzkosten', route: 'finance', note: 'Ein Katalog-/Freitextfeld, Live-Summe und Datumskurzwege statt doppelter Eingaben.', open: click('+ Neu') },
  { id: 'form-agreement', label: 'Neue Pauschale', route: 'persons/1', note: 'Betrag/Zeitraum und Gültigkeitsdaten paarweise; Gefährt-Verwaltung bleibt sichtbar.', open: click('+ Pauschale') },
  { id: 'form-payment', label: 'Neue Zahlung', route: 'persons/1', note: 'Kompaktes Feldraster und Datumskurzwege; Zuordnungslogik unverändert.', open: click('+ Zahlung') },
  { id: 'form-user', label: 'Benutzer bearbeiten', route: 'users', note: 'Kompakte Stammdaten; optionale Passwortänderung hinter einer eigenen Aufklappzeile.', open: async page => {
    await page.locator('.u-head').first().click(); await page.locator('.u-edit').first().click();
  } },
  { id: 'form-tariff', label: 'Tarif geöffnet', route: 'tariffs', note: 'Monats-/Jahrespreise zusammen; optionale Standardmaße separat aufklappbar.', open: async page => {
    await page.locator('.cfg-head').first().click();
  } },
);
const sha256 = value => createHash('sha256').update(value).digest('hex');
const git = (...args) => execFileSync('git', args, { cwd: root, windowsHide: true, maxBuffer: 24 * 1024 * 1024 });
const types = { '.html': 'text/html', '.css': 'text/css', '.js': 'application/javascript', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.png': 'image/png', '.webmanifest': 'application/manifest+json' };

function revisionAssets(ref) {
  const commit = git('rev-parse', '--verify', ref + '^{commit}').toString().trim();
  const files = new Set(git('ls-tree', '-r', '--name-only', commit, '--', 'web/static').toString().trim().split(/\r?\n/));
  const cache = new Map();
  const get = name => {
    if (!files.has(name)) return null;
    if (!cache.has(name)) cache.set(name, git('show', `${commit}:${name}`));
    return cache.get(name);
  };
  return { commit, get, hashes: Object.fromEntries(['index.html', 'js/app.js', 'css/style.css'].map(name => [name, sha256(get('web/static/' + name))])) };
}

async function captureVersion(browser, version, assets, capture) {
  const context = await browser.newContext({ viewport: { width: capture.width, height: capture.height },
    colorScheme: capture.theme, reducedMotion: 'reduce', locale: 'de-DE', timezoneId: 'Europe/Vienna', serviceWorkers: 'block', deviceScaleFactor: 1 });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.clock.setFixedTime(new Date(fixedTime));
  await page.addInitScript(expectedOrigin => {
    if (location.origin !== expectedOrigin) return;
    localStorage.setItem('parkrr-veh', '0'); localStorage.setItem('parkrr_portal_lang', 'de');
  }, origin);
  const records = await mockUI(page, { role: 'admin', overrides });
  // Both HTML and every local asset come from the selected commit, never a mixture.
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) return route.abort();
    if (url.pathname.startsWith('/api/')) return route.fallback();
    const name = 'web/static' + (url.pathname === '/' ? '/index.html' : decodeURIComponent(url.pathname));
    const data = assets.get(name);
    return route.fulfill(data ? { contentType: types[path.extname(name)] || 'application/octet-stream', body: data } : { status: 404, body: '' });
  });
  // Capture the public login screen without logging out or submitting a form.
  let login = false;
  await page.route(origin + '/api/auth/me', route => route.fulfill(login
    ? { status: 401, json: { error: 'unauthorized' } }
    : { json: { id: 1, username: 'Demo', role: 'admin', is_admin: true } }));
  const results = [];
  try {
    for (const surface of surfaces) {
      login = surface.id === 'login';
      // Hash-only navigation would retain the preceding app/auth/dialog state.
      await page.goto('about:blank');
      await page.goto(origin + '/#/' + surface.route);
      const readySelector = login ? '#login-form' : surface.id === 'portal' ? '#portal-view .portal-wrap' : surface.id === 'hall-1' ? '#page .gp' : '#page h2';
      await expect(page.locator(readySelector).first(), `${version}/${surface.id} ready`).toBeVisible();
      await expect(page.locator('.route-error')).toHaveCount(0);
      await page.waitForLoadState('networkidle');
      if (surface.open) {
        await surface.open(page);
        await expect(page.locator(surface.id === 'form-tariff' ? '.tf-panel.open' : '#modal')).toBeVisible();
      }
      await page.evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
      const file = `captures/${version}/${capture.id}-${surface.id}.png`;
      const screenshot = await page.screenshot({ path: path.join(output, file), fullPage: !surface.open });
      results.push({ version, capture: capture.id, surface: surface.id, file, width: screenshot.readUInt32BE(16), height: screenshot.readUInt32BE(20), sha256: sha256(screenshot) });
      assert.deepEqual(errors, [], `${version}/${surface.id}: uncaught browser error`);
    }
    assert.equal(records.filter(record => !['GET', 'HEAD'].includes(record.method)).length, 0, 'Captures must not submit forms');
    console.log(`${version} ${assets.commit.slice(0, 7)} · ${capture.id}: ${results.length} Ansichten aufgenommen`);
    return results;
  } finally { await context.close(); }
}

async function verifyGallery(browser, data) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const url = 'http://gallery.parkrr.test/index.html';
  await page.route('http://gallery.parkrr.test/**', async route => {
    const file = path.resolve(output, '.' + new URL(route.request().url()).pathname);
    if (!file.startsWith(output + path.sep)) return route.abort();
    try { await route.fulfill({ contentType: types[path.extname(file)] || 'application/octet-stream', body: await fs.readFile(file) }); }
    catch { await route.fulfill({ status: 404, body: '' }); }
  });
  await page.goto(url);
  let combinations = 0;
  for (const baseline of ['previous', 'original']) for (const capture of captures) for (const surface of surfaces) {
    await page.locator('#baseline').selectOption(baseline);
    await page.locator('#viewport').selectOption(capture.id);
    await page.locator('#page').selectOption(surface.id);
    await expect(page.locator('#pair')).toHaveAttribute('aria-busy', 'false');
    await expect(page.locator('#status')).toContainText('Beide Aufnahmen geladen');
    assert.equal(await page.locator('#before').getAttribute('src'), `captures/${baseline}/${capture.id}-${surface.id}.png`);
    assert.equal(await page.locator('#after').getAttribute('src'), `captures/current/${capture.id}-${surface.id}.png`);
    for (const [side, version] of [['before', baseline], ['after', 'current']]) {
      const expected = data.images.find(img => img.version === version && img.capture === capture.id && img.surface === surface.id);
      // Full-page historical captures preserve any original horizontal overflow.
      assert.deepEqual(await page.locator('#' + side).evaluate(img => [img.naturalWidth, img.naturalHeight]), [expected.width, expected.height]);
    }
    combinations++;
  }
  await page.goto(url + '#surface=form-person&baseline=previous&capture=light-1440');
  await expect(page.locator('#status')).toContainText('Beide Aufnahmen geladen');
  await page.locator('#page').focus();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(page.locator('#page')).toBeFocused();
  const axe = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
  assert.deepEqual(axe.violations.filter(v => ['serious', 'critical'].includes(v.impact)), []);
  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({ width, height: 1000 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `Gallery overflow at ${width}px`);
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator('#page').selectOption('form-person');
  await expect(page.locator('#status')).toContainText('Beide Aufnahmen geladen');
  await page.screenshot({ path: path.join(output, 'gallery-desktop.png'), fullPage: true });
  await page.locator('#viewport').selectOption('dark-390');
  await page.locator('#page').selectOption('form-charge');
  await expect(page.locator('#status')).toContainText('Beide Aufnahmen geladen');
  await page.screenshot({ path: path.join(output, 'gallery-mobile-comparison.png'), fullPage: true });
  // Verify the recovery message, then restore the real source without changing files.
  await page.route('**/captures/current/*form-tariff.png', route => route.abort());
  await page.locator('#page').selectOption('form-tariff');
  await expect(page.locator('#status')).toContainText('Aufnahme konnte nicht geladen werden');
  // The delivered entry point also works directly from the filesystem, without a server.
  await page.goto(pathToFileURL(path.join(output, 'index.html')).href);
  await expect(page.locator('#status')).toContainText('Beide Aufnahmen geladen');
  assert.deepEqual(errors, []);
  await context.close();
  console.log(`Galerie geprüft: ${combinations} Kombinationen, Tastatur, Fehleranzeige, 320/390/1440 px, axe ohne schwere Funde.`);
  return { combinations, widths: [320, 390, 1440], axeSeriousCritical: 0, browserErrors: 0 };
}

async function main() {
  const assets = Object.fromEntries(Object.entries(revisions).map(([name, ref]) => [name, revisionAssets(ref)]));
  const data = { fixedTime, locale: 'de-DE', timezone: 'Europe/Vienna', captures,
    versions: Object.fromEntries(Object.entries(assets).map(([name, asset]) => [name, { commit: asset.commit, hashes: asset.hashes }])),
    surfaces: surfaces.map(({ open, ...surface }) => ({ ...surface, kind: open ? 'form' : 'page' })),
    fixtures: Object.fromEntries(await Promise.all(['ui-fixture.js', 'overhaul-fixture.js'].map(async name => [name, sha256(await fs.readFile(path.join(__dirname, 'helpers', name)))]))), images: [] };
  const browser = await chromium.launch();
  try {
    if (process.argv.includes('--verify-only')) {
      const html = await fs.readFile(path.join(output, 'index.html'), 'utf8');
      const embedded = html.match(/^  const comparison = (.+);$/m);
      assert.ok(embedded, 'Generated comparison data is missing; run a full capture first');
      const stored = JSON.parse(embedded[1]);
      assert.deepEqual(stored.versions, data.versions, 'Captured revisions must match the configured revisions');
      assert.deepEqual(stored.fixtures, data.fixtures, 'Fixtures changed; regenerate the captures');
      assert.equal(stored.images.length, Object.keys(revisions).length * captures.length * surfaces.length);
      for (const image of stored.images) {
        const file = path.resolve(output, image.file);
        assert.ok(file.startsWith(path.join(output, 'captures') + path.sep));
        assert.equal(sha256(await fs.readFile(file)), image.sha256, `Capture changed: ${image.file}`);
      }
      stored.validation = await verifyGallery(browser, stored);
      await fs.writeFile(path.join(output, 'manifest.json'), JSON.stringify(stored, null, 2) + '\n');
      console.log(`${stored.images.length} unveränderte Aufnahmen erneut geprüft.`);
      return;
    }
    for (const [version, asset] of Object.entries(assets)) for (const capture of captures) {
      data.images.push(...await captureVersion(browser, version, asset, capture));
    }
    const template = await fs.readFile(path.join(__dirname, 'helpers/before-after-gallery.html'), 'utf8');
    assert.equal(template.split('__COMPARISON_DATA__').length, 2);
    await fs.writeFile(path.join(output, 'index.html'), template.replace('__COMPARISON_DATA__', JSON.stringify(data).replace(/</g, '\\u003c')));
    data.validation = await verifyGallery(browser, data);
    await fs.writeFile(path.join(output, 'manifest.json'), JSON.stringify(data, null, 2) + '\n');
    console.log(`${data.images.length} Aufnahmen · ${path.join(output, 'index.html')}`);
  } finally { await browser.close(); }
}
if (require.main === module) main().catch(error => { console.error(error); process.exitCode = 1; });
