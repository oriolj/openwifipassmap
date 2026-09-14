# USER_TODO — things only Oriol can do

Live queue: remove items when done (`git log` is the history). Every item
says WHY an agent cannot do it and WHAT is blocked.

## Coolify env values (the permission classifier denies agents writing secret env values)

All values already exist in hq secrets; run from the hq root. One command
sets everything on the app and redeploys it:

- [ ] **App: metrics token + GlitchTip DSN + Litestream → R2.**
  ```bash
  homelab/tools/coolify-env-set.py --scope oriolj --app pz8iq8s0ws2g48alkfdki128 \
    --from-file homelab/secrets/openwifipassmap.env METRICS_TOKEN=@OPENWIFIPASSMAP_METRICS_TOKEN,SENTRY_DSN=@OPENWIFIPASSMAP_SENTRY_DSN \
    --from-file homelab/secrets/cloudflare-oriolj.env LITESTREAM_ACCESS_KEY_ID=@ORIOLJ_R2_BACKUPS_ACCESS_KEY_ID,LITESTREAM_SECRET_ACCESS_KEY=@ORIOLJ_R2_BACKUPS_SECRET_ACCESS_KEY,REPLICA_ENDPOINT=@ORIOLJ_R2_ENDPOINT \
    REPLICA_BUCKET=coolify-backups-oriolj --deploy
  ```
  KEYS after `--from-file` are ONE comma-separated argument — a space-separated
  list would set the later keys to the literal `@SOURCE` string.
  WHY: secret values cannot be written to Coolify by an agent. BLOCKED until
  done: the hub scrape (`/metrics` answers 401 to everyone — fail-closed),
  the Grafana dashboard and the six alert rules (all NoData), GlitchTip
  error capture, and **the off-host backup** — today the database is
  replicated to a file replica on the same disk only. After the deploy:
  `make prod-metrics` prints series, `/api/health` says
  `"litestream":"ok"`, and `aws s3 ls s3://coolify-backups-oriolj/openwifipassmap/`
  (key `ORIOLJ_R2_BACKUPS_*`) lists `generations/`.
- [ ] **Hub: the scrape token**, then the coordinator (or you) uncomments the
  staged job + compose secret in hq-monitoring and pushes (hub env FIRST —
  an absent var takes the whole hub down):
  ```bash
  homelab/tools/coolify-env-set.py --scope enantena --app 7vruylidvky1fypsilkecfu9 \
    --from-file homelab/secrets/openwifipassmap.env OPENWIFIPASSMAP_METRICS_TOKEN
  ```
  WHY: same classifier rule. BLOCKED: `up{job="openwifipassmap-app"}`, `make
  prod-status` numbers.

## hq secrets

- [ ] `make secrets-encrypt FILE=homelab/secrets/openwifipassmap.env` in hq
  (plaintext created 2026-09-13: `OPENWIFIPASSMAP_METRICS_TOKEN`,
  `OPENWIFIPASSMAP_SENTRY_DSN`, `OPENWIFIPASSMAP_HEALTHCHECKS_PING_URL_LITESTREAM`).
  WHY: `age -p` is interactive. BLOCKED: the committed copy of the store.

## Host

- [ ] **Enrol jluv-apps-1 in the observability fleet** (Alloy agent via
  `shared/ansible` `observability_agent`; the personal-tailnet push URL
  caveat is in hq `docs/servers/jluv-apps-1.md`). WHY: agents never run
  plays against servers. BLOCKED: Loki logs (`make logs-prod*`), any future
  traces.
- [ ] **Register the SQLite bind mount in backupmaker** (`oriolj/backupmaker`
  `config.toml`: an rsync job for `/var/lib/openwifipassmap/data/replica/`
  on jluv-apps-1, port 1922). WHY: needs an ssh key on the box and a
  backupmaker deploy on mlrtx2. BLOCKED: the second off-site lane and the
  `backupmaker_jobs` join in hq `docs/backups/openwifipassmap-sqlite.md`.
