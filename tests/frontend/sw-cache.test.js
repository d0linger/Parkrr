'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Runs the real service worker against in-memory fetch/Cache Storage stubs and
// reports what it intercepted and what it stored.
function loadWorker(networkResponse) {
  const source = fs.readFileSync(path.join(__dirname, '../../web/static/sw.js'), 'utf8');
  const handlers = {};
  const stored = [];
  const cache = {
    put: async (req) => { stored.push(req.url); },
    addAll: async () => {},
  };
  const context = {
    URL,
    self: {
      location: { origin: 'https://parkrr.test' },
      addEventListener: (type, fn) => { handlers[type] = fn; },
      skipWaiting: () => Promise.resolve(),
      clients: { claim: () => Promise.resolve() },
    },
    caches: {
      open: async () => cache,
      keys: async () => [],
      delete: async () => true,
      match: async () => undefined,
    },
    fetch: async () => networkResponse(),
    Response: { error: () => ({ ok: false }) },
  };
  vm.runInNewContext(source, context);
  return {
    stored,
    async request(url) {
      let responded = null;
      const waits = [];
      handlers.fetch({
        request: { method: 'GET', url: 'https://parkrr.test' + url, mode: 'no-cors' },
        respondWith: (p) => { responded = p; },
        waitUntil: (p) => { waits.push(p); },
      });
      if (responded) await responded;
      await Promise.all(waits);
      return { intercepted: responded !== null };
    },
  };
}

function response(cacheControl) {
  const headers = new Map(cacheControl ? [['cache-control', cacheControl]] : []);
  const res = { ok: true, headers: { get: (k) => headers.get(k.toLowerCase()) ?? null } };
  res.clone = () => res;
  return res;
}

test('a percent-encoded calendar path is excluded like /ical/', async () => {
  const sw = loadWorker(() => response(''));
  const r = await sw.request('/%69cal/dummy-revocable-token');
  assert.equal(r.intercepted, false, 'the worker must leave the capability request to the network');
  assert.deepEqual(sw.stored, []);
});

test('a no-store response is never written to Cache Storage', async () => {
  const sw = loadWorker(() => response('private, no-store'));
  await sw.request('/js/app.js');
  assert.deepEqual(sw.stored, [], 'no-store must not be cached on any path');
});

test('ordinary same-origin assets are still cached for offline use', async () => {
  const sw = loadWorker(() => response(''));
  await sw.request('/js/app.js');
  assert.deepEqual(sw.stored, ['https://parkrr.test/js/app.js']);
});
