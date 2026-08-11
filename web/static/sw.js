/* Parkrr service worker – offline shell with fresh-first assets. */
const CACHE = 'parkrr-v118';
const SHELL = [
    '/',
    '/css/style.css',
    '/js/app.js',
    '/manifest.webmanifest',
    '/icons/icon.svg',
    '/icons/icon-maskable.svg',
    '/fonts/hanken.woff2',
    '/fonts/manrope.woff2',
    '/fonts/jbmono.woff2',
];

self.addEventListener('install', (event) => {
    event.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (event) => {
    event.waitUntil(
        caches.keys().then((keys) =>
            Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))
        ).then(() => self.clients.claim())
    );
});

self.addEventListener('fetch', (event) => {
    const { request } = event;
    if (request.method !== 'GET') return;
    const url = new URL(request.url);

    // Never cache API calls – always go to the network.
    if (url.pathname.startsWith('/api/')) {
        return;
    }

    // Network-first for navigations and same-origin assets (JS/CSS/fonts/icons):
    // an online client always gets the latest deploy; the cache is only the offline
    // fallback. This avoids the classic PWA trap where a cache-first shell pins stale
    // JS/CSS until the cache name changes.
    event.respondWith(
        fetch(request).then((res) => {
            if (res && res.ok && url.origin === self.location.origin) {
                const copy = res.clone();
                // Keep the worker alive until the cache write finishes.
                event.waitUntil(caches.open(CACHE).then((c) => c.put(request, copy)).catch(() => {}));
            }
            return res;
        }).catch(() => caches.match(request).then((cached) => {
            if (cached) return cached;
            // Only fall back to the app shell for navigations; a failed sub-resource
            // (image/script/API) must not receive the HTML shell.
            if (request.mode === 'navigate') return caches.match('/');
            return Response.error();
        }))
    );
});
