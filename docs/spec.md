# OpenWifiPassMap — Product & Technical Spec

## Problem & goal

People constantly hunt for usable WiFi in cafés, libraries and public venues —
and the password is often scrawled on a chalkboard or known only to regulars.
**OpenWifiPassMap** is a crowdsourced, public directory of those networks: name,
password, location, notes, and optional quality signals (ping, down/up speed).

Find good WiFi nearby, open a shareable page for a spot, or take a whole region
offline with a CLI and connect from the terminal.

## Scope decision: public, not encrypted

This project began as an end-to-end-encrypted private vault (per-user encrypted
DBs, per-collection sharing, recovery phrases). That was **dropped**. Café
passwords are *meant to be shared*, so the product is a single global **public**
directory. Consequences:

- No encryption, no key management, no private vaults.
- A spot's `password` is public data.
- The only secret is a contributor's account login password (argon2id, server-side).

## Users & contribution model

- **Anyone** can browse and use the CLI to download — no account.
- **Adding/editing/deleting** a spot requires a lightweight account
  (username + password). Each spot records `created_by`.
- Moderation is light: edit/delete-your-own, an admin flag, and a `reports`
  table (`wrong_password` / `gone` / `spam` / `other`). Community "works/doesn't
  work" voting is a documented future enhancement.

## Core entities

- **Spot**: `venue_name`, `essid`, `password` (blank = open), `auth_type`
  (`wpa2|wpa3|wep|open`), `lat`, `lng`, `geohash`, `notes`, optional `ping_ms`,
  `down_mbps`, `up_mbps`, `created_by`, timestamps.
- **User**, **Session**, **Report** — see [data-model.md](data-model.md).

## Surfaces

1. **JSON API** (Go) — auth, spot CRUD, `nearby`, `area` (paginated), report.
   See [api.md](api.md).
2. **Public web** (server-rendered) — landing with geolocated nearby list, and a
   shareable `/s/{id}` page (the "share this spot" surface).
3. **Mobile app** (Capacitor + React) — geolocated nearby map/list, add spot,
   connect to WiFi (platform-limited; see [mobile-wifi-apis.md](mobile-wifi-apis.md)).
4. **CLI** (`wifispot`) — download an area, query offline, scan & connect.
   See [cli.md](cli.md).

## Non-goals (v1)

- Encryption / privacy of spot data (it's public).
- Real-time collaboration or per-user collections.
- Global crowdsourced trust scoring (reports only for now).
- Native WiFi auto-connect testing in CI (platform/native only).

## Quality bar / definition of done (MVP)

- Backend runs locally; `register → add spot → nearby` works and is auth-gated.
- Mobile app runs (Vite) and performs the same flow against the backend.
- Public web lists nearby + renders a shareable spot page.
- CLI syncs an area and serves `nearby` **offline**.
- Verified by Playwright e2e (`e2e/tests/mvp.spec.ts`) + Go unit tests.

See the other docs in this folder for component detail.
