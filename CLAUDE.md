# CLAUDE.md — OpenWifiPassMap

Guidance for AI agents working in this repo. Read this first.

## What this is

A crowdsourced directory of **public** WiFi spots (cafés, venues) with network
name, password, location, notes, optional speeds. Four parts: a Go+SQLite
backend (JSON API + server-rendered public web), a Capacitor/React mobile app, a
Go CLI (`wifispot`), and the public web served by the backend.

## The single most important fact

**Everything is public. There is NO encryption.** An earlier plan called for
end-to-end encryption with private per-user vaults; that was deliberately
**dropped**. Do not reintroduce encryption, key management, or private vaults.
The `password` field on a spot is a café WiFi password meant to be shared. The
only hashed secret in the system is a user's *account login password*
(argon2id, server-side).

## Conventions

- **Go 1.26**, standard `net/http` with Go 1.22+ `ServeMux` routing — minimal deps.
- **SQLite via `modernc.org/sqlite`** (pure Go, no cgo) so the CLI cross-compiles cleanly.
- **Primary keys are uuid4** (generated in `models.NewID()`), stored as TEXT.
- **Timestamps are unix milliseconds** (INTEGER).
- Auth: argon2id (`internal/auth`) + opaque bearer-token sessions in the DB.
- Mobile UI: **Tailwind + DaisyUI** (CDN for the prototype; compile for production).
- Run things via the **Makefile** (`make start`, `make tmux`, `make test`, …).

## Layout / where things live

| Path | Purpose |
|---|---|
| `cmd/server` | backend entrypoint (wires API + web, migrates, serves) |
| `cmd/wifispot` | CLI entrypoint (sync/nearby/scan/connect) |
| `internal/api` | JSON API handlers, auth middleware, pagination |
| `internal/web` | server-rendered public site (`templates/*.html` embedded) |
| `internal/store` | server SQLite layer (users, sessions, spots, reports) |
| `internal/cache` | CLI's offline SQLite cache (spots only, no FKs) |
| `internal/geo` | haversine, bounding box, geohash (shared) |
| `internal/auth` | argon2id hashing + token generation |
| `internal/apiclient` | CLI's HTTP client (area pagination) |
| `internal/wifi` | nmcli / networksetup scan + connect (native only) |
| `internal/models` | shared types + id/token helpers |
| `migrations/schema.sql` | the schema, embedded via `migrations.Schema` |

## Gotchas

- **Never silently truncate lists.** `/api/spots/nearby` is radius-capped with a
  documented hard cap (`nearbyResultsCap`); `/api/spots/area` is fully
  cursor-paginated (`next_cursor`) — the CLI loops until it's empty. Keep it that way.
- **Geo:** nearby = SQL bounding-box prefilter (`internal/geo.BoundingBox`) then
  haversine trim in Go. `Area` paginates on the *scanned* DB row count, not the
  post-haversine page length — don't "fix" that to use `len(page)`.
- **Mobile WiFi is platform-limited.** iOS cannot scan/list nearby SSIDs (no public
  API) — discovery is GPS+DB, joining is `NEHotspotConfiguration`. Android can scan
  (throttled) and bulk-add suggestions. See `docs/mobile-wifi-apis.md`. The
  `internal/wifi` scan/connect are native/desktop only and aren't covered by e2e.
- **Port 8080 is often taken** locally (syncthing). e2e uses 8744; `make start
  PORT=…` overrides.
- **CORS** is only enabled in dev (`-dev` / `DEV=1`), needed because the Vite dev
  server (5173) and backend (8080) are different origins.
- **SQLite is single-writer.** WAL + `busy_timeout` handle it; serialize writes.
  See `docs/sqlite.md`.

## How to run / verify

```bash
make start                 # backend + mobile dev
make test                  # go test ./... + Playwright e2e
cd e2e && npx playwright test --reporter=list
```

The e2e suite (`e2e/tests/mvp.spec.ts`) auto-starts both servers and drives:
register → add spot → see it nearby → reveal password, plus the public web
landing + shareable page. `e2e/tests/visual.spec.ts` writes screenshots to
`e2e/screenshots/`.
