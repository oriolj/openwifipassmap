# DEPLOY.md — where OpenWifiPassMap runs and how it is operated

Verified facts only (dates say when). The Coolify/SQLite mechanics and the
anonymous-volume footgun are explained in [docs/deployment.md](docs/deployment.md);
monitoring in [GRAFANA_AND_METRICS.md](GRAFANA_AND_METRICS.md); the metric
catalogue in [METRICS.md](METRICS.md); what only Oriol can do in
[USER_TODO.md](USER_TODO.md).

## Where it runs

| Item | Value |
|---|---|
| Server | **jluv-apps-1** (Hetzner, `91.98.122.198`, ssh `root` port 1922; Coolify name `oriolj-apps-1`, server uuid `fso0kwogs0k4ggog4k8ccwkg`) — hq `docs/servers/jluv-apps-1.md` |
| Coolify | **personal (oriolj) Coolify Cloud team**, application **`openwifipassmap`** uuid `pz8iq8s0ws2g48alkfdki128`, build pack **Dockerfile** (`/docker/Dockerfile`, base dir `/`) → blue-green deploys |
| Source | GitHub App source, `oriolj/openwifipassmap` branch `master`, auto-deploy on push, `watch_paths`: `cmd/**`, `internal/**`, `migrations/**`, `docker/**`, `web/**`, `go.mod`, `go.sum` (a Makefile/docs-only push does NOT deploy) |
| Live URL | `https://openwifipassmap.oriolj.com` (Let's Encrypt via Traefik, force-https; site + JSON API + `/admin`) |
| Port | `8080` (Port Exposes), app binds `0.0.0.0` |
| Storage | **bind mount** `/var/lib/openwifipassmap/data` → `/data` (`wifispot.db`, `-wal`, `-shm`, `replica/`) — verified with `docker inspect` 2026-09-14 |
| Health check | Coolify UI check **ON**, `GET /api/health` on 8080; image `HEALTHCHECK` on the same path. Answers `{"status","version","checks":{"db","litestream"}}`, 503 when SQLite does not answer |
| Release id | `SOURCE_COMMIT` build arg (Coolify passes it) → `internal/buildinfo` → `/api/health.version`, `X-App-Version` response header, `openwifipassmap_app_info{version}`, GlitchTip release |
| Labels | `oj.project=openwifipassmap`, `oj.env=prod`, `oj.service=web` on the resource's custom labels (set 2026-09-13) |

## Environment variables (by name — values live only in Coolify / hq secrets)

| Name | Set? | Purpose |
|---|---|---|
| `RESEND_API_KEY` | ✅ runtime | Transactional mail (verification, password reset) |
| `PUBLIC_BASE_URL` | ✅ runtime | Origin for links in emails (`https://openwifipassmap.oriolj.com`) |
| `HEALTHCHECKS_PING_URL_LITESTREAM` | ✅ runtime | healthchecks.io check `openwifipassmap-litestream` (pinged every 5 min by the watchdog) |
| `METRICS_TOKEN` | ⏳ | Bearer for `/metrics` (fail-closed: 401 to everyone while unset). hq secret `OPENWIFIPASSMAP_METRICS_TOKEN` |
| `SENTRY_DSN` | ⏳ | GlitchTip project `oriolj/openwifipassmap`. hq secret `OPENWIFIPASSMAP_SENTRY_DSN` |
| `LITESTREAM_ACCESS_KEY_ID` / `LITESTREAM_SECRET_ACCESS_KEY` / `REPLICA_BUCKET` / `REPLICA_ENDPOINT` | ⏳ | Off-host replica to R2 `coolify-backups-oriolj` (hq `cloudflare-oriolj.env` `ORIOLJ_R2_BACKUPS_*`, `ORIOLJ_R2_ENDPOINT`). Without them the entrypoint runs the **file replica only** |
| `REPLICA_PATH` | optional | Object prefix inside the bucket, default `openwifipassmap/wifispot.db` |
| `ADDR`, `DB_PATH`, `STATIC_DIR`, `LITESTREAM_METRICS_ADDR` | image defaults | `:8080`, `/data/wifispot.db`, `/app/static`, `127.0.0.1:9091` |
| `RESEND_FROM`, `BACKFILL_EMAIL`, `GEOCODE_URL`, `SENTRY_ENVIRONMENT` | optional | see `cmd/server/main.go` |

The one command that sets every ⏳ row and redeploys is in
[USER_TODO.md](USER_TODO.md). Env backup: `homelab/tools/coolify-env-backup.sh
--scope oriolj` → hq `homelab/secrets/coolify-envs-oriolj.env` (re-run after
the envs above are set).

## Deploying

- **Push to `master`** touching a watched path → Coolify builds the image
  (multi-stage: Go build with `SOURCE_COMMIT`, Tailwind/DaisyUI assets,
  alpine runtime with Litestream 0.3.14, non-root `app`) and swaps traffic
  when `/api/health` is green. Push-to-deploy proven: deployments with
  `is_webhook=true` on 2026-07-23 (commits `0c26bf7`, `b8b7b4b`).
- **Manual**: Coolify UI → Deploy, or
  `POST https://app.coolify.io/api/v1/deploy?uuid=pz8iq8s0ws2g48alkfdki128` with the personal token.
- **Verify after every deploy**: `curl -s https://openwifipassmap.oriolj.com/api/health`
  (version = the pushed SHA, `litestream: ok`), `make prod-status`, the
  Grafana dashboard, `docker logs` for `replicating to` lines.
- **One-offs**: `ssh -p 1922 root@91.98.122.198 'docker exec -it $(docker ps -q --filter name=pz8iq8s0) sh'`
  (the image has `litestream` and the server binary; no shell tooling beyond
  busybox). Admin promotion: `UPDATE users SET is_admin = 1 …` with any SQLite
  client against a copy of the file, never against the live WAL DB.

## Backups

The server always runs under `litestream replicate -exec /app/server`
(`docker/entrypoint.sh`), so every WAL segment is shipped within ~1 s:

| Replica | Where | Snapshot / retention | State |
|---|---|---|---|
| `file` | same disk, `/var/lib/openwifipassmap/data/replica/generations/<gen>/…` | 6 h / 72 h | live from the first deploy of this build (2026-09-14) — a consistent copy for a host-side rsync, **not** an off-host backup |
| `s3` (R2) | bucket `coolify-backups-oriolj`, prefix `openwifipassmap/wifispot.db/generations/<gen>/{snapshots,wal}/…`, endpoint `ORIOLJ_R2_ENDPOINT`, region `auto` | 1 h / 168 h | ⏳ envs pending (USER_TODO.md) |

Read/list with the bucket-scoped key (`cloudflare-oriolj.env`
`ORIOLJ_R2_BACKUPS_ACCESS_KEY_ID` / `_SECRET_ACCESS_KEY`, `ORIOLJ_R2_ENDPOINT`):

```bash
AWS_ACCESS_KEY_ID=… AWS_SECRET_ACCESS_KEY=… aws s3 ls s3://coolify-backups-oriolj/openwifipassmap/ --recursive --endpoint-url "$ORIOLJ_R2_ENDPOINT"
```

Restore:

- **Fresh volume / new host**: nothing to do — the entrypoint runs
  `litestream restore -if-db-not-exists -if-replica-exists` from the replica
  with the newest data before the server starts.
- **Point in time**: inside the container (or anywhere with the R2 key):
  `litestream restore -config /etc/litestream.yml -replica s3 -timestamp 2026-09-14T00:00:00Z -o /data/restored.db /data/wifispot.db`,
  stop the app, replace `wifispot.db` (delete `-wal`/`-shm`), start.
- **Drills**: 2026-09-14 local, file replica — user registered, `wifispot.db*`
  wiped, boot restored it (`openwifipassmap_users_total 1`). No R2 drill yet
  (no R2 replica yet). Register: hq `docs/backups/openwifipassmap-sqlite.md`.
- Anonymous-volume symptom (spots drop to 0 after a redeploy):
  [docs/deployment.md](docs/deployment.md) «SQLite persistence on Coolify».

## Monitoring (details in GRAFANA_AND_METRICS.md)

`/metrics` bearer-gated on the public origin → hub job `openwifipassmap-app`
(staged until the token is on both sides) → dashboard `OpenWifiPassMap` +
alert group `openwifipassmap`; GlitchTip `oriolj/openwifipassmap`;
healthchecks.io `openwifipassmap-litestream`; Gatus group `openwifipassmap`
on status.enacast.com; Talaia suite `openwifipassmap/surfaces` (every 30 min);
Umami "OpenWifiPassMap · app". Logs/traces wait for the host's Alloy agent.

## Status — done / pending

| Area | Item | State | Notes |
|---|---|---|---|
| Deploy | Dockerfile resources, GitHub-App source, push-to-deploy proven (`is_webhook`) | ✅ | Dockerfile app `pz8iq8s0…`, GitHub App source; `is_webhook=true` deployments 2026-07-23; watch_paths listed above (verified via API 2026-09-14) |
| Deploy | Release identifier (git SHA) visible in app / Sentry | ✅ | `SOURCE_COMMIT` → `/api/health.version`, `X-App-Version`, `app_info`, Sentry `release` (local image verified 2026-09-14; live after this deploy) |
| Deploy | Env values backed up (`coolify-env-backup.sh`, scope token) | ✅ | resource block present in hq `coolify-envs-oriolj.env` (2026-09-13); re-run `--scope oriolj` after the ⏳ envs are set, then re-encrypt |
| Access | Real hostname + DNS records + TLS (not sslip/pages.dev) | ✅ | `openwifipassmap.oriolj.com`, Let's Encrypt, HTTP/2 200 (2026-09-14) |
| Access | Admin path randomised; superuser created | ➖ / ✅ | `/admin` is session-gated to the admin account (first account = admin, `docs/deployment.md`); no separate path needed |
| Health | Coolify UI health check ON for every resource (list the OFF ones + path) | ✅ | ON, `/api/health` :8080, resource `running:healthy` (API 2026-09-14) |
| Health | Image HEALTHCHECK per role | ✅ | single role; `HEALTHCHECK` on `/api/health` in `docker/Dockerfile` |
| Backups | Coolify scheduled DB backup → R2 — or the in-stack backup sidecar. Notes MUST say WHERE | ⏳ | product-native Litestream: file replica live, **R2 replica pending the envs** (USER_TODO.md); bucket/prefix/credential names in «Backups» above |
| Backups | Last backup execution verified (date) | ⏳ | file replica verified locally 2026-09-14; prod evidence = `litestream_replica_wal_bytes` once scraped / container log |
| Backups | Restore tested (date) | ✅ (local) | 2026-09-14 file-replica drill on the image; R2 drill pending |
| Backups | DB PITR / WAL archiving (needed? configured?) | ✅ | that is what Litestream is (1 s sync, 7 d generations on R2 once live) |
| Backups | borgmatic for volumes/media on the host | ➖ | jluv-apps-1 has no borg client (server file); the replica dir is the rsync target instead |
| Backups | Registered in the backupmaker inventory | ⏳ | rsync job for `/var/lib/openwifipassmap/data/replica/` (USER_TODO.md) |
| Backups | Upload bucket (S3/R2/B2) named here with region + versioning | ➖ | no user uploads; R2 `coolify-backups-oriolj` (region auto) is the replica target only |
| Backups | hq asset note per database / volume / bucket in `docs/backups/` | ✅ | hq `docs/backups/openwifipassmap-sqlite.md` (2026-09-14, `status: partial`) |
| Jobs | Scheduler/cron/beat monitored by healthchecks.io pings | ✅ | no scheduler; the Litestream watchdog pings `openwifipassmap-litestream` (check created 2026-09-13, first ping after this deploy) |
| Observability | Logs shipped (`oj.*` labels → Loki; `make logs`) | ⏳ | labels on the resource ✅; host not enrolled (no Alloy) — Loki empty |
| Observability | Host observability agent (Alloy) on the server | ⏳ | jluv-apps-1 staged in the fleet (USER_TODO.md, hq server file) |
| Observability | `/metrics` + Prometheus scrape job + Grafana dashboard | ⏳ | endpoint ✅ (bearer, fail-closed); dashboard + job committed in hq-monitoring (job staged/commented); blocked on `METRICS_TOKEN` both sides |
| Observability | Alert rules (Grafana → Pushover) | ✅ (staged) | group `openwifipassmap`, 6 rules OK on NoData, committed 2026-09-14 (hub push by the coordinator) |
| Observability | Uptime check (Beszel host + the project's group on Gatus) | ✅ | Gatus group `openwifipassmap` (landing + nearby API); Talaia suite every 30 min |
| Observability | Error tracking (Sentry/GlitchTip DSN, release tagged) | ⏳ | GlitchTip project id 18 ✅, SDK wired with release ✅; `SENTRY_DSN` env pending |
| Observability | Traces (OTel → host Alloy → Tempo; `<Project> traces` dashboard) | ⏳ | dashboard `openwifipassmap-traces` committed; no OTel exporter in the app and no Alloy on the host |
| Product | Web analytics on web surfaces (self-hosted, cookieless) | ✅ | Umami "OpenWifiPassMap · app" `55619df2-…` in every template `<head>` (live after this deploy) |
| Product | Email: Resend sending domain + from address | ✅ | `RESEND_API_KEY` set; from `no-reply@oriolj.com` |
| Product | Third-party keys (LLM, payments, SEO…) set or explicitly off | ✅ | none needed; Nominatim geocoding is keyless (policy-compliant UA + throttle) |
| Sites | Comercial site deployed (Pages) + docs site (Starlight) | ➖ | the landing page is the app itself; no separate site |
| Docs | hq `docs/projects.md` row + server file Services row link here | ✅ / ⏳ | projects.md row exists; link to this file proposed to the coordinator (2026-09-14) |
