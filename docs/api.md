# HTTP API

Base path `/api`. JSON in/out. Auth via `Authorization: Bearer <token>`.
Implemented in [`internal/api`](../internal/api/api.go).

## Auth

### `POST /api/auth/register`
Body `{ "username": "...", "password": "..." }` (username ≥3, password ≥8).
→ `200 { "token", "user" }`. `409` if username taken.

### `POST /api/auth/login`
Body `{ "username", "password" }` → `200 { "token", "user" }`. `401` on bad creds.

### `POST /api/auth/logout` *(auth)*
Deletes the current session. → `204`.

## Spots

### `GET /api/spots/nearby?lat=&lng=&radius_km=`  *(public)*
Distance-sorted spots within the radius. Radius is **capped at 50 km**; results
are **capped at 200** (a documented hard bound — `capped: true` signals you hit it).

→ `200 { "results": Spot[], "count": int, "capped": bool }`

Each `Spot` includes `distance_km`.

### `GET /api/spots/area?lat=&lng=&radius_km=&cursor=`  *(public)*
Bulk download for an area (radius capped at 300 km). **Cursor-paginated** — page
size 200. Loop while `next_cursor` is non-empty.

→ `200 { "results": Spot[], "next_cursor": string }`

> Pagination note: `area` pages on the *scanned* DB rows inside the bounding box,
> then trims to the true circle by haversine — so a page may contain fewer than
> 200 results yet still hand back a `next_cursor`. Keep looping until it's `""`.

### `GET /api/spots/{id}`  *(public)*
→ `200 Spot` or `404`.

### `POST /api/spots`  *(auth)*
Body (`SpotInput`): `essid` (required), `lat`, `lng` (required), optional
`venue_name`, `password`, `auth_type` (default `wpa2`), `notes`, `ping_ms`,
`down_mbps`, `up_mbps`. → `201 Spot`. `400` on validation error.

### `PUT /api/spots/{id}`  *(auth, owner or admin)*
Same body as create. → `200 Spot`. `403` if not owner/admin, `404` if missing.

### `DELETE /api/spots/{id}`  *(auth, owner or admin)*
→ `204`. `403`/`404` as above.

### `POST /api/spots/{id}/report`  *(auth)*
Body `{ "reason": "wrong_password|gone|spam|other" }` → `202`.

## Misc

### `GET /api/health` → `200 { "status": "ok" }`

## Errors
Non-2xx responses are `{ "error": "message" }` with the appropriate status code.

## CORS
Permissive CORS (`*`) is enabled **only** in dev mode (`-dev` / `DEV=1`) so the
Vite dev server (`:5173`) can call the backend (`:8080`). Disabled in production.
