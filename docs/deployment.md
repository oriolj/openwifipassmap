# Deployment (Coolify + Litestream)

The backend is a single Go service that also serves the public web — deploy it as
a Coolify **Dockerfile** application (not Compose) to get **blue-green** deploys
(build new container → health-check → swap traffic, zero downtime).

## Image

[`docker/Dockerfile`](../docker/Dockerfile): multi-stage build, `CGO_ENABLED=0`
static binary, alpine runtime, non-root user, `/data` volume, `HEALTHCHECK` on
`/api/health`, `STOPSIGNAL SIGTERM`.

```bash
make docker-build      # docker build -f docker/Dockerfile -t wifispots:latest .
```

## Coolify setup

1. **Resource type**: Application → **Dockerfile** (keeps blue-green).
2. **Port Exposes**: `8080` (Coolify's Traefik routes internally on the Docker
   network — don't add port mappings or custom Traefik labels).
3. **Persistent storage**: mount a volume at `/data` for the SQLite file.
4. **Env vars** (mark secrets *runtime-only*):
   - `ADDR=:8080`, `DB_PATH=/data/wifispot.db`
   - Litestream: `LITESTREAM_ACCESS_KEY_ID`, `LITESTREAM_SECRET_ACCESS_KEY`,
     `REPLICA_BUCKET`, `REPLICA_ENDPOINT`
5. App listens on `0.0.0.0` (it does — `ADDR=:8080`), required for Traefik.

## Litestream (backup / restore)

To add continuous backup, install Litestream in the image and make it the
entrypoint wrapping the server (so the DB is restored on boot and replicated
live):

```dockerfile
# add to the runtime stage
COPY --from=litestream/litestream:0.3 /usr/local/bin/litestream /usr/local/bin/
COPY docker/litestream.yml /etc/litestream.yml
ENTRYPOINT ["litestream", "replicate", "-exec", "/app/server"]
```

Config: [`docker/litestream.yml`](../docker/litestream.yml). Replication lag stays
under ~1 s; on a fresh container Litestream restores the latest snapshot before
the server starts. See [sqlite.md](sqlite.md).

## Notes

- Don't define custom Docker networks in Coolify (causes intermittent 504s).
- A 502 on a small VPS is usually swap, not config — check `free -m` first.
- The mobile app is built/distributed separately (Capacitor → App Store / Play),
  not served by this image. Point its `VITE_API_BASE` at the deployed URL.
