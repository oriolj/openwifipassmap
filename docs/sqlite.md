# SQLite in production

How we run SQLite for the backend, and the reasoning behind it. Configured in
[`internal/store.Open`](../internal/store/store.go).

## Driver: `modernc.org/sqlite` (pure Go)

A cgo-free translation of SQLite to Go. Trade-offs vs `mattn/go-sqlite3`:

- ✅ **No cgo** → the CLI cross-compiles to linux/macOS × amd64/arm64 trivially
  (`CGO_ENABLED=0`), and Docker builds stay tiny static binaries.
- ✅ Single dependency, no system SQLite needed.
- ⚠️ Slightly slower than the C driver on heavy workloads — irrelevant at our scale.

## PRAGMAs we set

Applied as DSN query params on open:

| pragma | value | why |
|---|---|---|
| `journal_mode` | `WAL` | readers never block the single writer (and vice-versa) |
| `busy_timeout` | `5000` | wait up to 5 s for the write lock instead of erroring |
| `synchronous` | `NORMAL` | the WAL sweet spot — durable across app crashes, fast |
| `foreign_keys` | `ON` | enforce FK constraints (off by default in SQLite!) |
| `cache_size` | `-64000` | 64 MB page cache |
| `temp_store` | `MEMORY` | temp tables/indexes in RAM |

WAL + `synchronous=NORMAL` sustains tens of thousands of writes/sec on NVMe and
cuts p99 latency materially under concurrent readers.

## Concurrency model

SQLite has **one writer at a time**. WAL means concurrent reads are unaffected.
For our write volume (occasional spot adds/edits) this is plenty; `busy_timeout`
absorbs the rare contention. If you ever need genuine multi-writer horizontal
scaling, that's the signal to move to Postgres — not before.

## Backups: Litestream

[Litestream](https://litestream.io) streams each WAL frame to S3-compatible
object storage (Backblaze B2 / MinIO / S3), keeping replication lag < 1 s and
restoring on boot if the local file is missing. Wrap the server:

```
litestream replicate -exec "/app/server"
```

Config: [`docker/litestream.yml`](../docker/litestream.yml). The `/data` volume
carries `wifispot.db`, `-wal` and `-shm` together.

## Files on disk

`data/wifispot.db` (+ `-wal`, `-shm` in WAL mode). All three are gitignored.

## Sources

- [SQLite WAL](https://sqlite.org/wal.html)
- [Litestream docs](https://litestream.io/)
- "SQLite in Production" benchmarks & guides (2025–2026): WAL p99 wins,
  `synchronous=NORMAL` throughput, when to choose Postgres.
