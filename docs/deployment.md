# Deployment (Coolify + Litestream)

The backend is a single Go service that also serves the public web — deploy it as
a Coolify **Dockerfile** application (not Compose) to get **blue-green** deploys
(build new container → health-check → swap traffic, zero downtime).

## ⚠️ Pending operator actions

One-time steps still to do in the Coolify UI / provider dashboards. Tick and
prune as they're done.

- [ ] **Turn on backups (Litestream).** The image ships with Litestream but it
  only activates when credentials are set. Create an S3-compatible bucket
  (Backblaze B2 is the cheap default), then set in Coolify (mark the two keys
  **runtime-only**): `LITESTREAM_ACCESS_KEY_ID`, `LITESTREAM_SECRET_ACCESS_KEY`,
  `REPLICA_BUCKET`, `REPLICA_ENDPOINT`. Redeploy, then check the container logs
  for litestream replication lines. Until this is done **prod has no off-host
  backup**.
- [ ] **Fire-drill the restore once**: stop a staging container, wipe its
  `/data`, boot — it should restore from the bucket before serving.
- [ ] **Pin `PUBLIC_BASE_URL=https://openwifipassmap.oriolj.com`.** Without it
  email links derive the host from forwarded headers (works, but pinning is
  immune to Host-header games).
- [ ] **Confirm `RESEND_API_KEY` is set** (runtime-only). `RESEND_FROM` is
  optional (defaults to `no-reply@oriolj.com`).
- [ ] **After the next deploy, do one test signup** — it now sends a
  verification email (first real Resend traffic); click the `/verify` link and
  then try forgot-password for that account.
- [ ] **Re-check the `/data` persistent storage mount** after any resource
  changes — see the SQLite-persistence footgun below.
- [ ] Before public launch: Plausible analytics + compiled Tailwind CSS
  (tracked in [TODO.md](../TODO.md)).

## Image

[`docker/Dockerfile`](../docker/Dockerfile): multi-stage build, `CGO_ENABLED=0`
static binary, alpine runtime, non-root user, `/data` volume, `HEALTHCHECK` on
`/api/health`, `STOPSIGNAL SIGTERM`.

```bash
make docker-build      # docker build -f docker/Dockerfile -t openwifipassmap:latest .
```

## Coolify setup

1. **Resource type**: Application → **Dockerfile** (keeps blue-green).
2. **Port Exposes**: `8080` (Coolify's Traefik routes internally on the Docker
   network — don't add port mappings or custom Traefik labels).
3. **Persistent storage**: mount a volume at `/data` for the SQLite file —
   see [SQLite persistence on Coolify](#sqlite-persistence-on-coolify) below
   before deploying, this is a footgun.
4. **Env vars** (mark secrets *runtime-only*):
   - `ADDR=:8080`, `DB_PATH=/data/wifispot.db`
   - `PUBLIC_BASE_URL=https://openwifipassmap.oriolj.com` — origin used to build
     links in emails (password-reset magic links). When unset the server derives
     it per-request from the proxy's `X-Forwarded-Proto`/`X-Forwarded-Host`, so
     links already use the real host; set it explicitly to pin the origin and be
     immune to Host-header spoofing.
   - `RESEND_API_KEY` (**runtime-only secret**) — enables real email; when unset
     the server logs the email instead of sending it.
   - `RESEND_FROM` — optional sender; defaults to `no-reply@oriolj.com`.
   - `BACKFILL_EMAIL` — address stamped onto any pre-email account on first boot
     after the email migration; defaults to `oriolj@gmail.com`.
   - Litestream: `LITESTREAM_ACCESS_KEY_ID`, `LITESTREAM_SECRET_ACCESS_KEY`,
     `REPLICA_BUCKET`, `REPLICA_ENDPOINT`
5. App listens on `0.0.0.0` (it does — `ADDR=:8080`), required for Traefik.

## Admin access

There is no admin-creation endpoint. Two bootstrap rules (in
`internal/store`, covered by tests):

- **Fresh database**: the first account ever registered becomes the admin.
- **Existing database with no admin**: on boot, `EnsureAdmin` promotes the
  *oldest* account — on this deployment that is the operator's own account.

The admin sees the moderation console at `/admin` (log in on the map page
first; the console uses the same browser session). Further admins require a
manual `UPDATE users SET is_admin = 1 …` against the DB.

## Litestream (backup / restore)

Litestream is baked into the image. The entrypoint
([`docker/entrypoint.sh`](../docker/entrypoint.sh)) activates it only when
`LITESTREAM_ACCESS_KEY_ID` is set: it first restores the DB from the replica if
the local file is missing (fresh volume / new host), then runs the server under
`litestream replicate -exec` so every write streams to the bucket. Without
credentials (local docker, CI) the server runs plain.

Set in Coolify (runtime-only secrets): `LITESTREAM_ACCESS_KEY_ID`,
`LITESTREAM_SECRET_ACCESS_KEY`, `REPLICA_BUCKET`, `REPLICA_ENDPOINT`
(e.g. Backblaze B2 / MinIO). Misconfigured credentials make the container exit
loudly instead of running unreplicated — fix the secrets rather than removing
them.

Config: [`docker/litestream.yml`](../docker/litestream.yml). Replication lag stays
under ~1 s; on a fresh container Litestream restores the latest snapshot before
the server starts. See [sqlite.md](sqlite.md).

## SQLite persistence on Coolify

> **TL;DR** — Without an explicit **Persistent Storage** entry in the Coolify UI,
> every redeploy gives the container a brand-new empty `/data`. The Dockerfile's
> `VOLUME ["/data"]` directive does **not** save you.

### What goes wrong

The Dockerfile declares `VOLUME ["/data"]`. With no matching Persistent Storage
entry in Coolify, Docker honors the directive by creating an **anonymous volume**
each time the container is created (every redeploy → every container restart
that recreates the container). The volume name is a 64-char SHA hash; the old
volume becomes an orphan in `/var/lib/docker/volumes/` and the new container
boots with an empty `/data` and a freshly-migrated, empty `wifispot.db`. The DB
on disk survives, but nothing points at it any more.

We observed this on 91.98.122.198 — four orphan volumes containing past DB
snapshots, the live container mounted on a fifth (empty) anonymous volume:

```
docker inspect $CID --format '{{json .Mounts}}'
# Type: "volume", Name: "dc72846...3c6cf9"  ← hash, not human name = anonymous
ls /var/lib/docker/volumes/ | wc -l         # multiple orphans accumulate
```

This is a long-running, well-known Coolify issue, not anything specific to this
app: see [coollabsio/coolify#2376](https://github.com/coollabsio/coolify/issues/2376)
and [#5099](https://github.com/coollabsio/coolify/issues/5099). It bites both
Dockerfile and Compose deployments when the volume isn't explicitly declared in
the Coolify UI.

### Fix

In the Coolify UI for the Application:

1. **Storage** tab → **Add Persistent Storage**.
2. **Name**: `wifispot-data` (Coolify auto-prepends the app UUID, so the real
   Docker volume name is `wifispot-data-<uuid>` and it's stable across redeploys).
3. **Source Path**: leave blank → Coolify creates a named Docker volume. Or set
   a host path (e.g. `/var/lib/openwifipassmap/data`) to use a bind mount — then
   the DB is plainly visible from the host and trivial to `tar` / inspect.
4. **Destination Path**: `/data`.
5. **Save** → **Redeploy**. Verify with
   `docker inspect <cid> --format '{{(index .Mounts 0).Name}}'` — should now
   show a human-readable name, not a 64-char hash.

### Recovery if you've already lost data

The data isn't actually gone yet — Coolify never deletes the old anonymous
volumes. Find the newest orphan with a populated DB and copy it into the new
named volume **with the container stopped**:

```bash
ssh -p 1922 root@<vps>
# 1. List orphan volumes carrying a wifispot.db, with sizes + mtimes.
for v in $(docker volume ls -q --filter dangling=true); do
  f="/var/lib/docker/volumes/$v/_data/wifispot.db"
  [ -f "$f" ] && stat -c "%y  %s bytes  $v" "$f"
done | sort

# 2. Stop the app, then copy the orphan's contents into the new named volume.
docker stop <container>
cp -a /var/lib/docker/volumes/<orphan-hash>/_data/. \
      /var/lib/docker/volumes/<named-volume>/_data/
docker start <container>
```

### Defence in depth

Even with the fix, the volume only protects against container recreates. For VPS
loss / disk failure use **Litestream** (see below) — it replicates the SQLite
file to S3/B2 every second, restores on cold boot, and is independent of how
Coolify mounts `/data`.

## Notes

- Don't define custom Docker networks in Coolify (causes intermittent 504s).
- A 502 on a small VPS is usually swap, not config — check `free -m` first.
- The mobile app is built/distributed separately (Capacitor → App Store / Play),
  not served by this image. Point its `VITE_API_BASE` at the deployed URL.

## Future: could PocketBase replace the Go backend?

Open question, not a current plan. PocketBase is a single-file Go backend
(SQLite + REST API + auth + admin UI + realtime + file uploads) in a ~15 MB
binary; idle RSS is reported around 10–20 MB, ~50–100 MB under typical load —
roughly comparable to our current `cmd/server` (modernc.org/sqlite + net/http,
also small-RSS). The resource argument is mostly a wash.

What we'd lose by switching:
- Our hand-written API contract (`/api/spots/area` cursor pagination,
  `/api/spots/nearby` cap semantics, geo prefilter in `internal/geo`) doesn't
  map cleanly onto PocketBase's auto-generated collection endpoints.
- The CLI (`cmd/wifispot`) shares `internal/store` types with the server;
  PocketBase would force a separate client schema.
- Less control over the SQLite tuning (WAL, busy_timeout, prepared statements)
  unless we go down PocketBase's Go-extension path, at which point we've
  re-implemented half the server anyway.

What we'd gain: the admin UI, password-reset / email-verification flows,
file uploads, and realtime subscriptions — none of which the current
spec needs. Re-evaluate when/if any of those become a requirement.
