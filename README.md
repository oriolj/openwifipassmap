# WiFi Spots 📶

A crowdsourced directory of **public** WiFi — cafés, libraries, public venues —
with their network name, password, location and notes. Find good WiFi near you,
share a spot with a link, or take a whole area offline with the CLI.

> **Everything here is public by design.** These are café/venue passwords meant
> to be shared. There is no end-to-end encryption and no private vaults — an
> earlier E2E design was intentionally dropped in favour of a simple, open,
> crowdsourced directory.

## What's in the box

| Component | Stack | What it does |
|---|---|---|
| **Backend** | Go + SQLite | JSON API + a small server-rendered public web |
| **Mobile app** | Capacitor + React + Vite + TS + Tailwind/DaisyUI | Browse nearby spots, add spots, connect to WiFi |
| **Public web** | Go `html/template` + Tailwind/DaisyUI | Landing + shareable per-spot pages |
| **CLI** (`wifispot`) | Go | Download an area, query **offline**, scan & connect |

## Quickstart

```bash
make deps        # go mod tidy + npm install (mobile + e2e)
make start       # backend (:8080) + mobile dev server (:5173)
```

Then open the mobile app at <http://localhost:5173> and the public web at
<http://localhost:8080>.

> Port 8080 is often taken locally (e.g. by syncthing). Override it:
> `make start PORT=8744`.

### CLI

```bash
make cli-build
./bin/wifispot sync   --lat 41.39 --lng 2.17 --radius 200   # download an area
./bin/wifispot nearby --lat 41.39 --lng 2.17 --radius 5      # query offline
./bin/wifispot scan                                          # match in-range SSIDs
./bin/wifispot connect "Central-WiFi"                        # connect via cache
```

### Tests

```bash
make test-go     # Go unit tests
make e2e         # Playwright end-to-end (auto-starts backend + frontend)
make test        # both
```

## Repository layout

```
cmd/server        backend entrypoint (API + public web)
cmd/wifispot      CLI entrypoint
internal/         api, web, store, geo, auth, cache, apiclient, wifi, models
migrations/       schema.sql (embedded)
templates/        — (public web templates live in internal/web/templates)
mobile/           Capacitor + React app
e2e/              Playwright tests + screenshots
docs/             internal docs (see below)
docker/           Dockerfile + litestream.yml
```

## Docs

Deep-dives live in [`docs/`](docs/):

- [spec.md](docs/spec.md) — product + technical spec
- [architecture.md](docs/architecture.md) — components & data flow
- [data-model.md](docs/data-model.md) — schema
- [api.md](docs/api.md) — REST endpoints
- [sqlite.md](docs/sqlite.md) — running SQLite in production
- [geo.md](docs/geo.md) — nearby/area queries
- [mobile-wifi-apis.md](docs/mobile-wifi-apis.md) — iOS/Android WiFi capabilities & limits
- [cli.md](docs/cli.md) — the `wifispot` CLI
- [deployment.md](docs/deployment.md) — Coolify + Litestream

## License

TBD.
