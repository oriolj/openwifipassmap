# Data model

Source of truth: [`migrations/schema.sql`](../migrations/schema.sql) (embedded
via `migrations.Schema`). All ids are uuid4 TEXT; timestamps are unix ms INTEGER.

## `users`
Contributors. Browsing is anonymous; an account is only needed to add/edit/delete.

| column | type | notes |
|---|---|---|
| `id` | TEXT PK | uuid4 |
| `username` | TEXT UNIQUE (NOCASE) | the handle; no email/PII stored |
| `password_hash` | TEXT | argon2id PHC string (`internal/auth`) |
| `is_admin` | INTEGER | 0/1 |
| `created_at`, `updated_at` | INTEGER | unix ms |

## `sessions`
Opaque bearer tokens (revocable; simpler than JWT).

| column | type | notes |
|---|---|---|
| `token` | TEXT PK | 32 random bytes, hex |
| `user_id` | TEXT FK→users | ON DELETE CASCADE |
| `created_at`, `expires_at` | INTEGER | 30-day TTL |

## `spots`
A public WiFi location. **All fields are public.**

| column | type | notes |
|---|---|---|
| `id` | TEXT PK | uuid4 |
| `venue_name` | TEXT | e.g. "Blue Bottle Coffee" |
| `essid` | TEXT | network name (required) |
| `password` | TEXT | blank for open networks |
| `auth_type` | TEXT | `wpa2` \| `wpa3` \| `wep` \| `open` |
| `lat`, `lng` | REAL | exact coordinates |
| `geohash` | TEXT | precision 6 (~1.2 km), for clustering/cache keys |
| `notes` | TEXT | free text |
| `ping_ms` | INTEGER NULL | optional quality signal |
| `down_mbps`, `up_mbps` | REAL NULL | optional |
| `created_by` | TEXT FK→users | attribution / ownership |
| `created_at`, `updated_at` | INTEGER | unix ms |

Indexes: `(lat, lng)`, `geohash`, `created_by`.

## `reports`
Lightweight moderation signal.

| column | type | notes |
|---|---|---|
| `id` | TEXT PK | uuid4 |
| `spot_id` | TEXT FK→spots | ON DELETE CASCADE |
| `reason` | TEXT | `wrong_password` \| `gone` \| `spam` \| `other` |
| `reporter_user_id` | TEXT FK→users NULL | ON DELETE SET NULL |
| `created_at` | INTEGER | unix ms |

## CLI cache (separate)
`internal/cache` uses its own `spots` table (same columns, **no foreign keys**)
plus a `meta(key,value)` table for the last sync center. Downloaded spots keep
their server id (`INSERT OR REPLACE`).

## Future (not in v1)
- `votes` (works ✓ / failed ✗) for freshness scoring.
