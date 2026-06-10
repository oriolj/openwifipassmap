// OpenWifiPassMap service worker. Hand-rolled on purpose: there is no bundler
// and the precache list is ten stable URLs — see docs/pwa-offline.md for the
// full design (strategies table, versioning rules, why /api is never cached).
//
// BUMP CACHE_VERSION whenever the shell contents change (CSS rename, new
// vendored lib). Activation drops every cache that doesn't match.
const CACHE_VERSION = 1;
const SHELL_CACHE = `owpm-shell-v${CACHE_VERSION}`;
const TILE_CACHE = "owpm-tiles-v1";
const TILE_MAX_ENTRIES = 600; // ~6-15 MB; polite to OSM, fits iOS quota

const PRECACHE = [
  "/",
  "/manifest.webmanifest",
  "/static/app.css",
  "/static/vendor/leaflet.css",
  "/static/vendor/leaflet.js",
  "/static/vendor/leaflet.markercluster.js",
  "/static/vendor/MarkerCluster.css",
  "/static/vendor/MarkerCluster.Default.css",
  "/static/icons/icon-192.png",
  "/static/icons/icon-512.png",
];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(SHELL_CACHE).then((c) => c.addAll(PRECACHE)));
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys
          .filter((k) => k !== SHELL_CACHE && k !== TILE_CACHE)
          .map((k) => caches.delete(k)),
      ),
    ).then(() => self.clients.claim()),
  );
});

// The update toast posts SKIP_WAITING when the user accepts the new version.
self.addEventListener("message", (event) => {
  if (event.data && event.data.type === "SKIP_WAITING") self.skipWaiting();
});

const isTile = (url) => url.hostname.endsWith("tile.openstreetmap.org");

// Network-first with a timeout: fresh when online, cached when not.
async function networkFirst(request, timeoutMs) {
  const cache = await caches.open(SHELL_CACHE);
  try {
    const res = await Promise.race([
      fetch(request),
      new Promise((_, reject) => setTimeout(() => reject(new Error("timeout")), timeoutMs)),
    ]);
    if (res && res.ok) cache.put(request, res.clone());
    return res;
  } catch (_) {
    const hit = await cache.match(request);
    if (hit) return hit;
    // Last resort for navigations: the app shell. The map boots and can serve
    // spot data from the offline pack (IndexedDB).
    const shell = await cache.match("/");
    if (shell) return shell;
    throw _;
  }
}

// Cache-first for tiles with a FIFO trim (Cache keys() are insertion-ordered).
async function tileCacheFirst(request) {
  const cache = await caches.open(TILE_CACHE);
  const hit = await cache.match(request);
  if (hit) return hit;
  const res = await fetch(request);
  if (res.ok) {
    await cache.put(request, res.clone());
    const keys = await cache.keys();
    if (keys.length > TILE_MAX_ENTRIES) {
      await Promise.all(keys.slice(0, keys.length - TILE_MAX_ENTRIES).map((k) => cache.delete(k)));
    }
  }
  return res;
}

// Cache-first for same-origin static assets (precache hits resolve here too).
async function shellCacheFirst(request) {
  const hit = await caches.match(request);
  if (hit) return hit;
  const res = await fetch(request);
  if (res.ok && new URL(request.url).origin === self.location.origin) {
    const cache = await caches.open(SHELL_CACHE);
    cache.put(request, res.clone());
  }
  return res;
}

self.addEventListener("fetch", (event) => {
  const { request } = event;
  if (request.method !== "GET") return; // writes always hit the network
  const url = new URL(request.url);

  // API responses are viewer-specific (my_rating) — never SW-cached. Offline
  // spot data comes from the app-level IndexedDB pack instead.
  if (url.origin === self.location.origin && url.pathname.startsWith("/api/")) return;

  if (isTile(url)) {
    event.respondWith(tileCacheFirst(request));
    return;
  }
  if (request.mode === "navigate") {
    event.respondWith(networkFirst(request, 3500));
    return;
  }
  if (url.origin === self.location.origin) {
    event.respondWith(shellCacheFirst(request));
  }
});
