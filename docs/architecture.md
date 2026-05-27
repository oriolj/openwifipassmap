# Architecture

## Components

```
                         ┌──────────────────────────────┐
   Mobile app (Capacitor │  cmd/server (Go)             │
   React/Vite) ─────────▶│   ├─ internal/api  (JSON)    │
                         │   ├─ internal/web  (HTML)    │──▶ SQLite (WAL)
   Public web browser ──▶│   └─ internal/store          │     data/wifispot.db
                         └──────────────────────────────┘            │
                                       ▲                              │ Litestream
   CLI (wifispot) ─────────────────────┘                             ▼
     └─ internal/cache (own offline SQLite)                   S3-compatible backup
```

A **single Go module** holds both binaries (`cmd/server`, `cmd/wifispot`) so they
share `internal/geo`, `internal/models`, etc. The mobile app is a separate
Node/Vite project under `mobile/`.

## Backend request flow

1. `cmd/server` opens SQLite (`internal/store.Open`, sets WAL + pragmas), applies
   the embedded schema (`migrations.Schema`), builds a `http.ServeMux`.
2. `internal/api.Routes` registers `/api/...` (Go 1.22 method+pattern routing).
   `internal/web.Routes` registers `/{$}` (landing) and `/s/{id}` (share).
3. Everything is wrapped by an optional-CORS middleware (dev only) and a request
   logger.
4. `withUser` middleware loads the bearer-token user into the request context;
   handlers call `requireUser` for mutations and check ownership.

## Data layers

- **`internal/store`** — server-side: `users`, `sessions`, `spots`, `reports`,
  with foreign keys. Used by `cmd/server`.
- **`internal/cache`** — CLI-side: a standalone `spots` table (no user FK) so
  downloaded spots store verbatim by id. Used by `cmd/wifispot`.

Both reuse `internal/geo` for bounding-box + haversine proximity.

## Why these choices

- **Go + SQLite**: one binary, one file, trivial ops; `modernc.org/sqlite` is
  pure Go so the CLI cross-compiles without cgo. See [sqlite.md](sqlite.md).
- **Server-rendered public web**: shareable, crawlable spot pages without a JS app.
- **Capacitor + React** for mobile: one web codebase → iOS/Android, biggest
  plugin ecosystem. See [mobile-wifi-apis.md](mobile-wifi-apis.md).
- **No encryption**: the data is public; see [spec.md](spec.md).

## Deployment

Single service → Coolify **Dockerfile** resource (blue-green), with Litestream
streaming the SQLite file to S3-compatible storage. See [deployment.md](deployment.md).
