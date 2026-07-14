/* Parkrr service worker – offline shell caching. */
const CACHE = 'parkrr-v58';
const SHELL = [
    '/',
    '/css/style.css',
    '/js/app.js',
    '/manifest.webmanifest',
    '/icons/icon.svg',
    '/icons/icon-maskable.svg',
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

    // Network-first for the app shell / navigations, falling back to cache.
    if (request.mode === 'navigate') {
        event.respondWith(
            fetch(request).catch(() => caches.match('/'))
        );
        return;
    }

    // Cache-first for static assets.
    event.respondWith(
        caches.match(request).then((cached) =>
            cached || fetch(request).then((res) => {
                const copy = res.clone();
                caches.open(CACHE).then((c) => c.put(request, copy)).catch(() => {});
                return res;
            })
        )
    );
});
