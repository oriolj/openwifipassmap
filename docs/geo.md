# Geo: nearby & area queries

Implemented in [`internal/geo`](../internal/geo/geo.go) and used by both
`internal/store` (server) and `internal/cache` (CLI).

## Approach: bounding box + haversine

Spots are public, so exact `lat`/`lng` are stored as plain `REAL` columns with a
`(lat, lng)` index. A proximity query is two steps:

1. **SQL prefilter** by a bounding box (`geo.BoundingBox(lat, lng, radiusKM)`):
   `WHERE lat BETWEEN ? AND ? AND lng BETWEEN ? AND ?`. The index makes this fast.
2. **Trim in Go** with the great-circle distance (`geo.HaversineKM`) to the true
   circle, then sort by distance.

The bounding box is a *superset* of the circle (it includes the corners), so step
2 is required for an accurate radius. `BoundingBox` uses ~111 km/°latitude and
`111·cos(lat)` km/°longitude, clamps latitude to ±90, and widens to the whole
globe for absurdly large radii (guards `cos→0` near the poles).

## Pagination interplay (`area`)

The `area` endpoint cursor-paginates by `id` over the bounding-box rows. It tracks
the **scanned DB row count** to decide whether more pages remain — *not* the
post-haversine result length, because the haversine trim can drop corner rows and
make a page look short while more rows still exist. See [api.md](api.md).

## geohash

We also store a precision-6 geohash (~1.2 km cell) per spot. It's not used for the
nearby query itself (bbox+haversine is simpler and exact) but is handy for:

- map clustering,
- cache keys / dedupe in the CLI,
- a future server-side coarse prefilter at very large scale.

Library: [`github.com/mmcloughlin/geohash`](https://github.com/mmcloughlin/geohash).

## Alternatives considered

- **SQLite R\*Tree** module — a spatial index of bounding rectangles; supports
  circle/nearest queries. Worth adopting only if the `(lat,lng)` index ever
  becomes a bottleneck (large dataset). Our bbox+haversine is enough for now.
- **SpatiaLite** — full OGC spatial extension; far more than we need and adds a
  native dependency (kills the pure-Go cross-compile story). Rejected.

## Sources

- [SQLite R\*Tree module](https://www.sqlite.org/rtree.html)
- [SpatiaLite](https://en.wikipedia.org/wiki/SpatiaLite)
- Haversine formula (standard great-circle distance).
