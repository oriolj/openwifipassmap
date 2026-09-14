#!/bin/sh
# The server always runs under Litestream (`litestream replicate -exec`):
#   - with LITESTREAM_ACCESS_KEY_ID set → docker/litestream.yml: S3/R2 replica
#     (the off-host backup) + a local file replica;
#   - without it → docker/litestream-file.yml: local file replica only (a
#     consistent copy for the host-side rsync lane; same disk, not a backup).
# Before serving, restore the DB from the newest replica when the local file
# is missing (fresh volume / new host). Misconfigured S3 credentials make
# Litestream exit loudly instead of running unreplicated — fix the secrets,
# don't remove them.
set -e

DB="${DB_PATH:-/data/wifispot.db}"
export REPLICA_PATH="${REPLICA_PATH:-openwifipassmap/wifispot.db}"
export LITESTREAM_METRICS_ADDR="${LITESTREAM_METRICS_ADDR:-127.0.0.1:9091}"

if [ -n "$LITESTREAM_ACCESS_KEY_ID" ]; then
  CFG=/etc/litestream.yml
else
  CFG=/etc/litestream-file.yml
  echo "entrypoint: no LITESTREAM_ACCESS_KEY_ID — local file replica only (no off-host backup)" >&2
fi

litestream restore -config "$CFG" -if-db-not-exists -if-replica-exists "$DB"
exec litestream replicate -config "$CFG" -exec /app/server
