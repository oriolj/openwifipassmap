#!/bin/sh
# With Litestream credentials present: restore the DB from the replica when
# the local file is missing (fresh volume / new host), then run the server
# under litestream so every write is replicated continuously.
# Without credentials (local docker, CI): just run the server.
set -e

DB="${DB_PATH:-/data/wifispot.db}"

if [ -n "$LITESTREAM_ACCESS_KEY_ID" ]; then
  litestream restore -if-db-not-exists -if-replica-exists "$DB"
  exec litestream replicate -exec /app/server
fi

exec /app/server
