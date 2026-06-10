# PWA & offline support — design + implementation notes

The public web (landing map at `/`) is an installable, offline-capable PWA.
This document is the design rationale and the operational reference: read it
before touching `web/sw.js`, the manifest, or the offline-pack code in
`landing.html`.

**Why offline matters more here than for most apps:** the user looking for
WiFi *is the user without internet*. The whole point of the app peaks exactly
when connectivity is zero. Offline isn't a nice-to-have polish feature — it's
the core scenario.

## What works offline

| Surface | Offline behavior |
|---|---|
| Landing map `/` | Loads from the service-worker cache (app shell). Map tiles you've already seen render from the tile cache; unseen tiles are gray. |
| Spot data | Served from the **offline pack** (IndexedDB) if the user downloaded one; otherwise the last viewport isn't available and the status line says so. |
| Share pages `/s/{id}` | Render from cache only if previously visited. Otherwise the navigation falls back to the app shell (the map), where pack data may cover the spot. |
| Adding / rating / auth | Requires network by design. Buttons fail with a clear error; no write queueing (see "Non-goals"). |

## Architecture

Three layers, each independently useful:

1. **App shell cache** (service worker, Cache Storage) — the HTML + same-origin
   CSS/JS needed to boot the map with zero network. All assets were made
   same-origin first (compiled Tailwind in `/static/app.css`, vendored Leaflet
   in `/static/vendor/`) precisely so this layer can be complete. A PWA that
   still pulls `cdn.tailwindcss.com` at runtime can never boot offline.
2. **Tile cache** (service worker, runtime) — OpenStreetMap tiles cached as
   they're fetched, capped (see below). Gives "the map looks right where I've
   already been".
3. **Offline pack** (application code, IndexedDB) — an explicit, user-triggered
   download of every spot within **200 km** of the current map center. This is
   deliberately *not* the service worker's job: spot data is paginated,
   personalised (`my_rating` when authed), and needs geo-queries — Cache
   Storage is the wrong tool. The app owns this data and falls back to it when
   `fetch` fails.

### Why the split between SW and IndexedDB

The service worker caches *requests*. The offline pack stores *data*. Caching
`/api/spots/area?lat=…&lng=…&cursor=…` responses verbatim would only replay
the exact viewports the user happened to look at, page by page, and would go
stale per-URL. The pack instead walks the cursor API once, stores the merged
result set, and the app filters it by whatever viewport the user pans to while
offline. One download, any viewport within the radius.

## The service worker (`web/sw.js` → served at `/sw.js`)

Hand-rolled, ~150 lines, no Workbox. Justification (the default advice is "use
Workbox"): there is no bundler and no hashed-filename build manifest here —
the precache list is ten stable URLs. Workbox's value (precache manifests,
revision hashing, route builders) mostly doesn't apply; its cost (a CDN dep or
a build step in a deliberately build-light stack) does. The three behaviors we
need are small enough to own — but they must stay small. If this file grows
past ~250 lines, revisit Workbox.

### Scope & registration

- The SW file is served at **`/sw.js`** (Go route, `Cache-Control: no-cache`)
  so its scope is the whole origin. The map at `/` *is* the product surface;
  share/reset/verify pages benefit from the same shell fallback.
- Registered from the landing page only (`navigator.serviceWorker.register('/sw.js')`).
  Share pages don't register it — but once installed it serves them too.

### Cache strategy per resource (the table that matters)

| Resource | Strategy | Cache name | Bound |
|---|---|---|---|
| Precache shell (`/`, `/static/app.css`, vendored leaflet JS/CSS, icons, manifest) | **Cache-first**, repopulated wholesale on SW version bump | `owpm-shell-v{N}` | fixed list |
| Navigations (`request.mode === "navigate"`) | **Network-first, 3.5 s timeout → cached page → cached `/` shell** | `owpm-shell-v{N}` | — |
| OSM tiles (`*.tile.openstreetmap.org`) | **Cache-first** | `owpm-tiles-v1` | ~600 entries, FIFO-trimmed |
| `/api/*` | **Pass-through, never cached** | — | — |
| Everything else same-origin (`/static/*` not in precache) | Cache-first, runtime-filled | `owpm-shell-v{N}` | small |

Why `/api/*` is never SW-cached: responses are viewer-specific when a bearer
token is attached (`my_rating`, `confirmed_by_me`). A shared Cache Storage
keyed only by URL could leak one account's fields to another on a shared
device. The offline pack handles API offline-ness at the app layer instead,
where it's an explicit user action.

### Versioning & update flow

- `CACHE_VERSION` constant at the top of `sw.js`. **Bump it whenever the shell
  contents change** (new vendored lib, renamed CSS). Activation deletes every
  `owpm-shell-*` / `owpm-tiles-*` cache that doesn't match the current names —
  the equivalent of Workbox's `cleanupOutdatedCaches`.
- Updates are **prompted, never silent**: when a new SW is installed while an
  old one controls the page, the landing page shows a toast — "New version
  available · Reload". Accepting posts `{type:"SKIP_WAITING"}` to the worker
  and reloads on `controllerchange`. No auto-reload (hostile mid-interaction).
- `/sw.js` itself is served with `Cache-Control: no-cache` so deploys are
  picked up on the next visit (browsers also re-check SW scripts every 24 h
  regardless).

### Tile cache sizing

OSM tiles are ~10–25 KB each. The 600-entry cap ≈ 6–15 MB — comfortably under
iOS Safari's ~50 MB pre-prompt ceiling, even alongside the shell and a large
pack. Trimming is oldest-first by insertion order (Cache Storage `keys()` is
insertion-ordered) after each tile write; approximate LRU is fine here.
Also respect OSM's tile-usage policy: the cap keeps us a polite consumer.

## The offline pack (IndexedDB, app-owned)

### Download flow (`downloadOfflinePack()` in `landing.html`)

1. User taps **“⬇ Offline”** in the list header. (Explicit user action — never
   download ~MBs of data behind someone's back, they may be on metered data.)
2. The app walks `GET /api/spots/area?lat={c.lat}&lng={c.lng}&radius_km=200`
   following `next_cursor` until exhausted — **no page cap**, unlike the
   viewport loader. Progress is streamed to the status line
   ("Downloading… N spots").
3. The result is stored in IndexedDB (`owpm` DB, `packs` store, single record
   under key `"area"`):

   ```js
   { center: {lat, lng}, radiusKm: 200, savedAt: epoch-ms, spots: [...] }
   ```

   One blob, not row-per-spot: the pack is read all-at-once into memory on
   fallback (a 200 km radius in a dense city is a few thousand spots ≈ a few
   MB of JSON — trivial), and replacing it atomically on refresh is a
   single `put`. Row-level indexing would buy nothing.
4. Re-tapping the button refreshes the pack at the *current* center (the old
   pack is replaced, not merged — merging two stale circles creates data of
   unknowable freshness).

### Why 200 km

The server caps `radius_km` at 300 (`areaMaxRadius`). 200 km covers a full
metropolitan region + day trips around it while leaving margin under the cap,
and keeps the pack comfortably inside mobile storage budgets. It is a
constant (`OFFLINE_PACK_RADIUS_KM`) in `landing.html` — changing it is a
one-line edit; values >300 will be clamped by the server.

### Fallback path

`loadSpots()` tries the network first, always (freshest data wins). Only when
the *first page* fetch throws (offline / DNS dead) does it call
`loadPackFallback()`, which:

1. Reads the pack from IndexedDB (absent → status explains how to download).
2. Filters pack spots to the current map bounds (client-side bbox test).
3. Renders through the same `renderSpots()` path — filters, colored pins,
   cards all work identically offline.
4. Sets the status line to
   "offline — N spot(s) from your downloaded pack (saved X ago)".

Auth-dependent fields inside a pack (`my_rating`) reflect whoever downloaded
it; reviews/edits are disabled offline anyway (the POST fails), so this is
display-only staleness, acceptable.

### Pack staleness

The pack keeps `savedAt` and surfaces its age in the UI every time it's used
or shown. No auto-refresh: refreshing several MB on every visit punishes
metered connections for data that changes slowly. The user refreshes when
they care (e.g. before a trip).

## Installability

`/manifest.webmanifest` (Go route with `Content-Type: application/manifest+json`):

- `name` "OpenWifiPassMap", `short_name` "WifiMap" (≤12 chars),
- `start_url: "/"`, `scope: "/"`, `display: "standalone"`,
- `theme_color: "#10b981"` (matches `<meta name="theme-color">` in the page),
  `background_color: "#ffffff"`,
- icons: 192, 512, and a 512 `purpose: "maskable"` (Android adaptive masks
  crop ~20% of a non-maskable icon). Source PNGs live in `web/icons/`
  (checked in); the build copies them to `/static/icons/`.

Chrome's install criteria: HTTPS ✓ (prod) / localhost ✓ (dev), manifest with
the fields above ✓, registered SW with a `fetch` handler ✓.

## iOS Safari quirks (tested behaviors, 2026)

- **No `beforeinstallprompt`** — users install via Share → "Add to Home
  Screen". Nothing to trigger programmatically.
- Safari prefers **`<link rel="apple-touch-icon">`** over manifest icons —
  the landing page sets it to `icon-192.png`.
- `theme_color` in the manifest is ignored; the **`<meta name="theme-color">`**
  tag is honored.
- Storage: Cache Storage + IndexedDB are evictable under pressure and capped
  around ~50 MB before prompting; our worst case (shell ~1 MB + tiles ≤15 MB +
  pack a few MB) fits. **Installed-PWA storage can still be purged by iOS
  after weeks of disuse** — the pack may silently vanish; the fallback
  handles "no pack" gracefully for exactly this reason.
- Web Push: only for installed PWAs on iOS 16.4+; not used by this app today.

## Non-goals (deliberate)

- **No offline write queue.** Queuing adds conflict semantics (what if the
  spot was edited meanwhile?) for a rare need. Offline users read; writes
  error clearly and immediately.
- **No Background Sync / periodic pack refresh.** Spotty browser support and
  surprises on metered connections. The refresh button is explicit.
- **No tile pre-download for the pack area.** 200 km of tiles at useful zooms
  is gigabytes. Tiles cache opportunistically as browsed, capped.
- **No SW on the mobile (Capacitor) app.** The native bundle ships its own
  assets; a SW there only serves stale JS across app-store updates.

## Operational notes

- **Bump `CACHE_VERSION`** in `web/sw.js` when shell assets change. Deploys
  that only change server code or API behavior don't need a bump (navigations
  are network-first; HTML is always fresh online).
- `make css` (or any `make start`/e2e run) copies `sw.js` + manifest from
  `web/` into `internal/web/static/`; the Go server serves them at root paths
  (`/sw.js`, `/manifest.webmanifest`) with no-cache headers.
- **Verifying after a deploy**: DevTools → Application → Service Workers
  (running, scope `/`), Cache Storage shows `owpm-shell-v{N}`; Network →
  Offline → reload → map boots; Lighthouse → PWA → installable green.
- e2e coverage (`e2e/tests/pwa.spec.ts`): manifest + SW served with correct
  content types; SW registers and precaches; offline pack downloads; with
  `context.setOffline(true)` the page reloads from cache and renders spots
  from the pack.

## File map

| File | Role |
|---|---|
| `web/sw.js` | the service worker (copied to `/static/sw.js`, served at `/sw.js`) |
| `web/manifest.webmanifest` | manifest (served at `/manifest.webmanifest`) |
| `web/icons/*` | source icons (copied to `/static/icons/`) |
| `internal/web/web.go` | routes: `/sw.js`, `/manifest.webmanifest` (no-cache) |
| `internal/web/templates/landing.html` | SW registration, update toast, offline pack (IndexedDB), offline fallback in `loadSpots` |
| `docs/pwa-offline.md` | this document |
