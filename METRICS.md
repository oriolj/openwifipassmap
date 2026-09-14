# METRICS.md — the `/metrics` catalogue and access contract

The Go server exposes Prometheus metrics on `GET /metrics`
(`internal/metrics`). The human-facing "what does Grafana show and how do I
act on it" doc is [GRAFANA_AND_METRICS.md](GRAFANA_AND_METRICS.md); deploy
state is [DEPLOY.md](DEPLOY.md).

## Access

- **Bearer token, fail-closed.** Every request must carry
  `Authorization: Bearer $METRICS_TOKEN`. With `METRICS_TOKEN` unset the
  endpoint answers **401 to everyone** unless the server runs with `-dev` /
  `DEV=1` (local runs). The same value is `OPENWIFIPASSMAP_METRICS_TOKEN` on
  the hq-monitoring hub resource (Prometheus job `openwifipassmap-app`,
  `credentials_file: /run/secrets/openwifipassmap_metrics_token`). Value:
  hq `homelab/secrets/openwifipassmap.env`.
- Scraped on the **public https origin** (`https://openwifipassmap.oriolj.com/metrics`):
  jluv-apps-1 is not on the hub's tailnet and a Coolify Dockerfile app has no
  host port (blue-green). `/metrics` and `/api/health` are excluded from the
  request histogram so the probe and the scrape never drown real traffic.
- Dedicated registry: the exposition holds exactly the families below plus
  Go runtime (`go_*`) and process (`process_*`) series, then Litestream's own
  `litestream_*` families appended verbatim (pass-through from the
  in-container replicator's `127.0.0.1:9091`, `internal/litestream`).

## Families (`openwifipassmap_` prefix)

### HTTP golden signals (`Instrument` middleware, outside the mux)

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `http_request_duration_seconds` | histogram | `route`, `method`, `code` | Latency per **mux pattern** (`GET /api/spots/nearby`, `POST /api/spots` …); `route="unmatched"` for 404s outside the route table. Buckets 5 ms … 10 s. |
| `http_requests_in_flight` | gauge | — | Requests being served right now. |
| `rate_limited_total` | counter | `route` | 429s from the per-IP token buckets (auth, geocode, forgot-password, resend-verification). |

### Product / feature counters (call sites)

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `geocode_requests_total` | counter | `result` = `cache` \| `upstream` \| `busy` \| `error` | Address searches: served from the in-memory cache, fetched from Nominatim, refused because the 1 req/s outbound throttle queue was full, or failed upstream. All four label values are pre-created (0 instead of absent). |
| `emails_total` | counter | `kind` = `verification` \| `reset`, `result` = `sent` \| `error` | Transactional mail via Resend (sent from a goroutine, so a failure shows here and in the log, never in the request). |

### Business snapshot (per-scrape collector over SQLite, `internal/metrics/store.go`)

Computed on every scrape with small indexed aggregates (single-digit ms on
this database); a restart never resets a value because the source is the
durable store.

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `spots` | gauge | `quality` = `0`…`3` | Spots in the directory by cached quality (0 = unrated, 3 = great). `sum()` = the directory size — the panel and alert that catch the anonymous-volume symptom (a redeploy booting on an empty `/data`). |
| `spots_created` | gauge | `window` = `24h` \| `7d` | Spots created in the trailing window. |
| `users_total` / `users_verified_total` | gauge | — | Accounts / accounts with a verified email. |
| `users_created` | gauge | `window` = `24h` \| `7d` | Sign-ups in the trailing window. |
| `reviews_total` / `confirmations_total` | gauge | — | Reviews (rating and/or speed) and "still works" confirmations, one per spot+user. |
| `reports_total` | gauge | — | **Open** moderation reports (rows are deleted when handled). |
| `sessions_active_total` | gauge | — | Unexpired login sessions. |
| `db_file_bytes` | gauge | `file` = `db` \| `wal` \| `shm` | On-disk size of the SQLite files. WAL mode: the `.db` file alone is not the database. |
| `collector_duration_seconds` | gauge | — | Time the snapshot took on this scrape. |
| `collector_errors_total` | counter | — | Scrapes on which the snapshot failed (the business series are then absent). |

### Replication and release

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `litestream_up` | gauge | — | 1 when the in-container Litestream metrics listener answered on the last watchdog tick (replicator alive). Absent when the server does not run under Litestream. |
| `litestream_s3_configured` | gauge | — | 1 when `LITESTREAM_ACCESS_KEY_ID` is present, i.e. an **off-host** (R2) replica is configured. 0 = local file replica only = not a backup. |
| `litestream_heartbeat_pings_total` | counter | `result` = `ok` \| `fail` \| `ping_error` | healthchecks.io pings sent by the watchdog (check `openwifipassmap-litestream`, every 5 min). |
| `app_info` | gauge | `version` | Always 1; `version` is the deployed git SHA (12 chars, from the `SOURCE_COMMIT` build arg). |

### Litestream pass-through (`litestream_*`, no app prefix)

Litestream's own families, filtered to `litestream_*` (its `go_*`/`process_*`
are dropped so they never collide with the app's): `litestream_db_size`,
`litestream_wal_size`, `litestream_sync_count`, `litestream_sync_error_count`,
`litestream_replica_wal_bytes{name=s3|file}`, `litestream_replica_wal_index`,
`litestream_replica_wal_offset`, `litestream_replica_validation_total`, …
(all labelled `db="/data/wifispot.db"`, replica series also `name`).

## Verifying

```bash
make prod-metrics                          # bearer from hq secrets; prints the HTTP code when empty
curl -s -o /dev/null -w '%{http_code}\n' https://openwifipassmap.oriolj.com/metrics   # 401 = token gate live
make docker-build && make docker-run       # local image: DEV=1 opens /metrics without a token
```

Adding a metric: declare it in `internal/metrics/metrics.go` (or a field in
`store.Stats` + a `Desc` in `store.go`), register it in `init()`, add the row
here, then the panel in the hub dashboard and — if it should page — the rule
(see GRAFANA_AND_METRICS.md «How to change things»).
