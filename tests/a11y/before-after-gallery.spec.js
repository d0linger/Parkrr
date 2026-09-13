const fs = require('node:fs/promises');
const path = require('node:path');
const { test, expect } = require('@playwright/test');

const origin = 'http://gallery.parkrr.test';
const comparison = {
  versions: { original: { commit: '1111111' }, previous: { commit: '2222222' }, current: { commit: '3333333' } },
  surfaces: [
    { id: 'form-charge', kind: 'form', label: 'Zusatzkosten', note: 'Kompaktes Formular' },
    { id: 'persons', kind: 'page', label: 'Personen', note: 'Personenübersicht' },
  ],
  captures: [
    { id: 'light-1440', width: 1440, label: 'Desktop' },
    { id: 'dark-390', width: 390, label: 'Mobil' },
  ],
};
const pixel = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64');

test.use({ serviceWorkers: 'block' });
test.beforeEach(async ({ page }) => {
  const template = await fs.readFile(path.join(__dirname, 'helpers/before-after-gallery.html'), 'utf8');
  const html = template.replace('__COMPARISON_DATA__', JSON.stringify(comparison).replace(/</g, '\\u003c'));
  await page.route('**/*', route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) return route.abort();
    if (url.pathname === '/review/index.html') return route.fulfill({ contentType: 'text/html', body: html });
    if (/^\/review\/captures\/(original|previous|current)\/(light-1440|dark-390)-(form-charge|persons)\.png$/.test(url.pathname)) {
      return route.fulfill({ contentType: 'image/png', body: pixel });
    }
    return route.abort();
  });
});

async function expectCapture(page, version, capture = 'light-1440', surface = 'form-charge') {
  await expect(page.locator('#status')).toContainText('Beide Aufnahmen geladen');
  for (const [side, revision] of [['before', version], ['after', 'current']]) {
    const file = `captures/${revision}/${capture}-${surface}.png`;
    await expect(page.locator('#' + side)).toHaveAttribute('src', file);
    await expect(page.locator('#' + side + '-link')).toHaveAttribute('href', file);
    await expect(page.locator('#' + side + '-revision')).toHaveText(comparison.versions[revision].commit);
  }
  await expect(page.locator('#pair')).toHaveAttribute('aria-busy', 'false');
}

test('gallery preserves supported comparisons, selections and hash navigation', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(origin + '/review/index.html');
  await expectCapture(page, 'previous');
  await page.locator('#baseline').selectOption('original');
  await expectCapture(page, 'original');
  await page.locator('#page').selectOption('persons');
  await page.locator('#viewport').selectOption('dark-390');
  await expectCapture(page, 'original', 'dark-390', 'persons');
  await expect(page.locator('#pair')).toHaveClass('pair mobile');
  await page.reload();
  await expectCapture(page, 'original', 'dark-390', 'persons');
  expect(errors).toEqual([]);
});

test('untrusted hash values resolve to known capture paths without becoming markup', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const payload = '<img src=x onerror="window.galleryInjected=true">';
  const hash = new URLSearchParams({ baseline: 'javascript:alert(1)', surface: payload, capture: '../../outside' });
  await page.goto(origin + '/review/index.html#' + hash);
  await expectCapture(page, 'previous');
  await expect(page.locator('img')).toHaveCount(2);
  expect(await page.evaluate(() => window.galleryInjected)).toBeUndefined();
  expect(errors).toEqual([]);
});

test('unexpected DOM baseline values cannot enter capture URLs or break the gallery', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(origin + '/review/index.html');
  await expectCapture(page, 'previous');
  for (const value of ['../../outside', 'javascript:alert(1)', '<img src=x onerror=alert(1)>', 'current', '']) {
    await page.evaluate(value => {
      const baseline = document.getElementById('baseline');
      baseline.add(new Option('Untrusted option', value));
      baseline.value = value;
      baseline.dispatchEvent(new Event('change', { bubbles: true }));
    }, value);
    await expectCapture(page, 'previous');
    expect(new URLSearchParams(new URL(page.url()).hash.slice(1)).get('baseline')).toBe('previous');
  }
  expect(errors).toEqual([]);
});
