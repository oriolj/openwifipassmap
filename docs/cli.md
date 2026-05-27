# `wifispot` CLI

Offline-first terminal client for the public directory. Entrypoint:
[`cmd/wifispot`](../cmd/wifispot/main.go); WiFi ops in
[`internal/wifi`](../internal/wifi/wifi.go); local cache in
[`internal/cache`](../internal/cache/cache.go).

## Build

```bash
make cli-build          # ./bin/wifispot for this platform
make cli-release        # dist/wifispot-{linux,darwin}-{amd64,arm64}
```

Pure-Go SQLite ⇒ `CGO_ENABLED=0` cross-compiles cleanly to all four targets.

## Cache location

`$XDG_DATA_HOME/wifispot/cache.db` (default `~/.local/share/wifispot/cache.db`).
Override per command with `--db <path>`. Server URL defaults to
`http://localhost:8080` or `$WIFISPOT_SERVER`.

## Commands

### `wifispot sync --lat <> --lng <> [--radius 200] [--server URL] [--db PATH]`
Downloads public spots within `radius` km of the point, following the API's
`next_cursor` until done, and upserts them into the cache (by id). Records the
center as the last-sync location.

### `wifispot import [--server URL] [--token T | --username U] <file.csv>`
Uploads spots from a CSV to the server (this **writes** to the public DB, so it
requires auth). The CSV header must include at least `essid`, `lat`, `lng`;
optional columns: `venue_name`, `password`, `auth_type`, `notes`. Every row must
have the same column count as the header. Rows missing essid/lat/lng are skipped;
malformed rows are reported and counted but don't abort the run; each valid row
is POSTed to `/api/spots`. See [`seed/pending-spots.csv`](../seed/pending-spots.csv)
for the format.

**Credentials** — never pass a password as an argv flag (it leaks via `ps` and
shell history). Use one of:

```bash
# Preferred: a token from the environment
export WIFISPOT_TOKEN=…           # obtain via /api/auth/login
wifispot import seed/pending-spots.csv

# Or log in by username; the password is read from the env or a no-echo prompt
export WIFISPOT_PASSWORD=…        # optional; omit to be prompted
wifispot import --server http://localhost:8080 --username oriolj seed/pending-spots.csv
```

**Idempotent**: re-running the same import does not create duplicates — the
server returns the existing spot when the same user already has one with the same
SSID at the same coordinates. (`http://` to a non-local host warns, since
credentials would travel in cleartext — use `https://`.)

### `wifispot nearby --lat <> --lng <> [--radius 5] [--db PATH]`
Queries the **local cache** (works fully offline) and prints spots sorted by
distance, with passwords.

```
DIST(km)  VENUE         SSID          SECURITY  PASSWORD
0.0       Cafe Central  Central-WiFi  wpa2      espresso
1.7       Park Kiosk    Park-Free     open      (open)
```

### `wifispot scan [--db PATH]`
Scans in-range networks and marks which ones we have a cached password for.
- Linux: `nmcli -t -f SSID dev wifi list` (needs NetworkManager).
- macOS: best-effort via `system_profiler SPAirPortDataType` (the `airport` CLI
  was removed in macOS 14.4, so scanning is unreliable there).

### `wifispot connect <ssid> [--db PATH]`
Looks up the SSID's password in the cache and connects.
- Linux: `nmcli dev wifi connect <ssid> password <pw>`.
- macOS: `networksetup -setairportnetwork en0 <ssid> <pw>`.

## Typical session

```bash
wifispot sync --lat 41.39 --lng 2.17 --radius 200   # at home, online
# …later, offline at a café …
wifispot nearby --lat 41.3851 --lng 2.1734           # what's around?
wifispot scan                                        # what's actually in range?
wifispot connect "Central-WiFi"                      # join it
```

## Notes
- `scan`/`connect` are native/desktop only and not exercised by CI/e2e (no WiFi
  hardware in the sandbox), but the command wiring is covered by `sync`/`nearby`.
- The cache uses a foreign-key-free schema so downloaded spots store verbatim.
