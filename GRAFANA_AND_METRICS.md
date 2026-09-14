# GRAFANA_AND_METRICS.md — what we watch on OpenWifiPassMap and what it means

Human-facing monitoring reference (fleet-observability skill §5e). The
metric catalogue is [METRICS.md](METRICS.md); deploy state and the status
table are in [DEPLOY.md](DEPLOY.md). Kept current with every dashboard,
alert or scrape change — the change log is at the end.

## 1. Where everything is

| What | Where |
|---|---|
| Hub | `monitor-1-nc` (hq-monitoring repo, Coolify compose stack; Prometheus + Loki + Tempo + Grafana) |
| Grafana | `http://monitor-1-nc:3000` (tailnet): dashboards in org **Personal** (id 2), folder `openwifipassmap`; alert rules in org **hq** (id 1), folder `hq` (same split as every personal project) |
| Dashboards | `OpenWifiPassMap` (uid `openwifipassmap`, `grafana/dashboards/personal/openwifipassmap/openwifipassmap.json`) and `OpenWifiPassMap traces` (uid `openwifipassmap-traces`, same dir) |
| Prometheus job | `openwifipassmap-app` — `prometheus/prometheus.yml`, **STAGED (commented)** until the token is on both sides (see §6) |
| Alert group | `openwifipassmap` — `grafana/provisioning/alerting/openwifipassmap.yml` (6 rules, all OK on NoData) |
| Scrape token | app env `METRICS_TOKEN` = hub env `OPENWIFIPASSMAP_METRICS_TOKEN` (compose secret `openwifipassmap_metrics_token`, staged). Value: hq `homelab/secrets/openwifipassmap.env` |
| Logs | Loki `{project="openwifipassmap", env="prod"}` — **empty today**: jluv-apps-1 has no Alloy agent (hq `docs/servers/jluv-apps-1.md`, «Not in the observability fleet»). `make logs-prod*` work once the host is enrolled |
| Traces | Tempo, `service.namespace="openwifipassmap"` — **nothing flows yet**: the Go server is not OTel-instrumented and the host has no Alloy OTLP intake. The traces dashboard is provisioned so it lights up the day both exist |
| Error tracking | GlitchTip org `oriolj`, project **openwifipassmap** (id 18, platform go). DSN = `SENTRY_DSN` on the app (hq secret `OPENWIFIPASSMAP_SENTRY_DSN`); release = git SHA, environment `prod` |
| Heartbeat | healthchecks.io (ORIOLJ project) check **`openwifipassmap-litestream`** — period 10 min, grace 15 min; pinged every 5 min by the in-app Litestream watchdog (`HEALTHCHECKS_PING_URL_LITESTREAM` on the app) |
| Uptime | Gatus group `openwifipassmap` on `status.enacast.com` (`hq-monitoring/gatus/config/personal/openwifipassmap.yaml`: landing + nearby API); Talaia suite `openwifipassmap/surfaces` every 30 min (`~/git/oriolj/talaia/backend/projects/openwifipassmap/surfaces`) |
| Web analytics | Umami `stats.oriolj.com`, website "OpenWifiPassMap · app" (`55619df2-89ab-45ba-aca2-6fc9c63552f1`), tag in every template `<head>` |
| Terminal | `make prod-status`, `make prod-metrics`, `make logs-prod*`, `make slow-requests` … (Makefile «Production observability») |

## 2. The data path

```
jluv-apps-1 (Coolify Dockerfile app pz8iq8s0…, blue-green, no host port)
  container: litestream replicate -exec /app/server
     ├─ /app/server  GET /metrics (bearer) ──── https://openwifipassmap.oriolj.com/metrics ──▶ hub Prometheus (job openwifipassmap-app)
     │      ├─ openwifipassmap_* (HTTP histogram, product counters, per-scrape SQLite snapshot, app_info)
     │      └─ litestream_* passed through from ──┐
     ├─ litestream metrics 127.0.0.1:9091 ◀────────┘  (watchdog every 5 min → healthchecks.io ping ok|fail)
     ├─ panics / 5xx ────────────────────────────────▶ GlitchTip oriolj/openwifipassmap (release = SHA)
     └─ stdout access log (key=value)  ─ ─ ─ ─ ─ ─ ─▶ Loki  (DASHED: no Alloy on this host yet)
  /data (bind mount /var/lib/openwifipassmap/data): wifispot.db + -wal + replica/ (file replica) ─ ─▶ R2 (DASHED: creds pending)
```

## 3. The dashboard, row by row (`OpenWifiPassMap`)

| Row | Panels | How to read | Series |
|---|---|---|---|
| Product — the directory | Spots (total, by quality), spots created 24h/7d, users total/verified, sign-ups 24h/7d, reviews, confirmations, open reports, active sessions | The KPIs. A **sudden drop of spots to ~0** is the anonymous-volume symptom (a redeploy booted on an empty `/data`) — DEPLOY.md «SQLite persistence». Open reports > 0 = moderation work at `/admin`. | `openwifipassmap_spots`, `_spots_created`, `_users_total`, `_users_verified_total`, `_users_created`, `_reviews_total`, `_confirmations_total`, `_reports_total`, `_sessions_active_total` |
| Key features | Nearby searches/h, uploads (201 on `POST /api/spots`)/24h, geocode by result, rate-limited by route, emails by kind/result | Feature usage from the route histogram. `geocode result=busy` rising = the 1 req/s Nominatim throttle is saturated; `error` = upstream down. `emails result=error` = Resend failing (log line has the reason). | `_http_request_duration_seconds_count` filtered by `route`, `_geocode_requests_total`, `_rate_limited_total`, `_emails_total` |
| HTTP — golden signals | req/s by route, 5xx ratio, p50/p95/p99, in-flight, status codes | Health and metrics routes are excluded. p95 above ~250 ms on `/api/spots/nearby` means the bounding-box scan is missing its index. | `_http_request_duration_seconds_*`, `_http_requests_in_flight` |
| SQLite and Litestream | db/wal/shm bytes, collector duration/errors, `litestream_up`, `litestream_s3_configured`, sync count, sync errors, replica WAL bytes per replica, heartbeat pings by result | **`s3_configured` = 0 means the database is NOT backed up off the box** (file replica on the same disk only). Sync errors > 0 = credentials or bucket trouble. WAL growing without bound = a long-running reader is blocking checkpoints. | `_db_file_bytes`, `_collector_*`, `_litestream_*`, `litestream_*` |
| Process — Go runtime | goroutines, heap, GC, open fds, CPU | Baseline; a goroutine count climbing steadily is a leak in a handler. | `go_*`, `process_*` |

Blind spots: the access log has no status code (5xx come from the
histogram); the histogram cannot see time spent in Traefik; without Loki
there is no per-request log search from the workstation (ssh + `docker logs`
until the host is enrolled).

## 4. Alerts (group `openwifipassmap`, folder `hq`, evaluated every 1 m)

Severity routing is the hub's (Pushover: `critical` immediately, `important`
/ `warning` grouped). Every rule is **OK on NoData** so the file could ship
before the scrape job; a missing scrape is `hq-target-down`'s job.

| uid | Condition | for | Severity | Means / first move |
|---|---|---|---|---|
| `hq-target-down` (global) | `up{job="openwifipassmap-app"} == 0` | 5 m | important | The public origin stopped answering `/metrics` with the bearer: app down (Coolify status, `/api/health`), token rotated on one side only, or Traefik/TLS. |
| `owpm-web-5xx` | 5xx share > 5 % with > 0.02 req/s | 10 m | important | SQLite writer failing (disk full, locked DB) or a handler panic — GlitchTip has the exception; `make prod-status`. |
| `owpm-spots-dropped` | `sum(spots)` < 50 % of its 7-day median | 5 m | important | Empty `/data` after a redeploy (anonymous volume) or a real mass delete. DEPLOY.md «SQLite persistence» recovery; the old files are still on the host / in the replica. |
| `owpm-litestream-down` | `litestream_up == 0` | 10 m | warning | The replicator died inside the container; the server keeps serving unreplicated. `docker logs` for `litestream` lines; a redeploy restarts it. healthchecks.io goes red on the same condition. |
| `owpm-litestream-sync-errors` | `increase(litestream_sync_error_count[1h]) > 0` | 15 m | warning | R2 credentials expired/revoked, bucket unreachable, or the file replica's disk is full. Fix the cause; replication resumes by itself. |
| `owpm-no-offsite-replica` | `litestream_s3_configured == 0` | 6 h | warning | Standing reminder: the R2 envs are not set on the resource (USER_TODO.md). Not a page. |
| `owpm-collector-failing` | `increase(collector_errors_total[30m]) > 0` | 15 m | warning | The per-scrape SQLite snapshot failed — DB locked or unreadable; `/api/health` carries the db check. |

### 4b. GlitchTip

Project `oriolj/openwifipassmap`. The SDK is initialised only when
`SENTRY_DSN` is set (dormant otherwise); `serverErr` captures every 500 and
the `sentryhttp` middleware every panic (re-panicked so net/http still logs
it). Performance tracing is off (`TracesSampleRate: 0`) — traces belong to
Tempo.

### 4c. healthchecks.io

`openwifipassmap-litestream`: the watchdog (`internal/litestream`) polls the
replicator's metrics every 5 min and pings the success URL when it answered
and no new sync error appeared since the last tick, `/fail` otherwise. A dead
process cannot ping, so the check goes red on silence too (10 min period,
15 min grace). Notifications follow the ORIOLJ project's channels.

## 5. Logs

Selector: `{project="openwifipassmap", env="prod"}` (`oj.project=openwifipassmap`,
`oj.env=prod`, `oj.service=web` are on the Coolify resource's custom labels
since 2026-09-13). Targets: `make logs-prod`, `make logs-prod-grep Q=…`,
`make logs-prod-errors` (`level=ERROR|panic|litestream.*error`),
`make logs-prod-5xx` (from Prometheus, the access log has no status).
**Empty until jluv-apps-1 runs the Alloy agent** — until then:
`ssh -p 1922 root@jluv-apps-1 'docker logs --tail 200 $(docker ps -q --filter name=pz8iq8s0)'`.

## 6. How to change things

- **Add a metric**: `internal/metrics` → METRICS.md row → panel in the
  dashboard JSON (`allowUiUpdates: false`, the JSON is the truth) → rule if it
  pages. Deploy the app BEFORE a rule that references a new series.
- **Turn the scrape on** (once): set `METRICS_TOKEN` on the app and
  `OPENWIFIPASSMAP_METRICS_TOKEN` on the hub resource (same value, hq
  `homelab/secrets/openwifipassmap.env`; `coolify-env-set.py` lines in
  USER_TODO.md) — the hub env FIRST — then uncomment the compose secret
  (both the `secrets:` entry and the `- openwifipassmap_metrics_token` line
  under prometheus) and the job in `prometheus.yml`, `make smoke`, push
  hq-monitoring, verify `up{job="openwifipassmap-app"} == 1` and
  `make prod-status`.
- **Rotate the token**: new value in the secrets file → hub env → app env →
  redeploy both. The app fails closed, so a mismatch shows as
  `hq-target-down`, never as an open endpoint.
- **Change an alert**: edit the YAML, push the hub (rules reload on startup;
  deleting a rule needs its uid tombstoned per the hub README), verify on
  Grafana → Alert rules.
- **Coolify domain change**: regenerates the Traefik labels — re-check that
  the three `oj.*` labels survived (`GET /applications/{uuid}`).

## 7. Known gaps (baseline §5d)

- Logs and traces: host not in the fleet (no Alloy); the Go server has no OTel
  exporter. Both are one enrolment + one `otelhttp` middleware away.
- Off-host backup: R2 envs pending (Litestream file replica only).
- No worker/scheduler rows (single process) — `logs-worker`, `prod-top-queries`
  answer "not applicable".
- Access log without status code.

## 8. Change log

- 2026-09-13 — `/metrics` (bearer, fail-closed) with HTTP histogram, product
  counters, SQLite snapshot collector, Litestream pass-through + watchdog +
  healthchecks.io check; GlitchTip project; dashboard `openwifipassmap` +
  `openwifipassmap-traces`; alert group (6 rules); scrape job staged; Umami tag;
  `oj.*` labels on the resource; observability `make` targets.
- 2026-09-14 — code reviewed, built and restore-drilled locally (file
  replica), committed and deployed; docs (this file, METRICS.md, DEPLOY.md,
  USER_TODO.md) written; hub files committed (push by the coordinator).
- 2026-09-14 — deploy `d249d95` verified live (push-triggered, healthy,
  Litestream file replica + watchdog pinging healthchecks.io, Umami tag in the
  HTML); staged compose secret + smoke canary committed in hq-monitoring;
  `make routes` clamps the default window to 23 h (Tempo's metrics cap).
