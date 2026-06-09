-- OpenWifiPassMap schema. Everything is public by design; no encryption.
-- Timestamps are unix milliseconds (INTEGER).

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    email         TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);
-- Note: email is intentionally NOT UNIQUE — one person may own several accounts
-- under one address, and the backfill of pre-email accounts deliberately shares
-- a single address. Its index is created in store.EnsureUserEmail, after the
-- column is guaranteed to exist (an existing DB only gains it via ALTER there).

-- Single-use, time-limited tokens backing the password-reset magic links.
-- Raw token in the row (same trust model as sessions); consumed by DELETE.
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_prt_user ON password_reset_tokens(user_id);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS spots (
    id         TEXT PRIMARY KEY,
    venue_name TEXT NOT NULL DEFAULT '',
    essid      TEXT NOT NULL,
    password   TEXT NOT NULL DEFAULT '',
    auth_type  TEXT NOT NULL DEFAULT 'wpa2',
    lat        REAL NOT NULL,
    lng        REAL NOT NULL,
    geohash    TEXT NOT NULL DEFAULT '',
    notes      TEXT NOT NULL DEFAULT '',
    ping_ms    INTEGER,
    down_mbps  REAL,
    up_mbps    REAL,
    quality    INTEGER NOT NULL DEFAULT 0, -- manual rating: 0=unrated, 1=basic, 2=good, 3=great
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_spots_latlng ON spots(lat, lng);
CREATE INDEX IF NOT EXISTS idx_spots_geohash ON spots(geohash);
CREATE INDEX IF NOT EXISTS idx_spots_created_by ON spots(created_by);

CREATE TABLE IF NOT EXISTS reports (
    id               TEXT PRIMARY KEY,
    spot_id          TEXT NOT NULL REFERENCES spots(id) ON DELETE CASCADE,
    reason           TEXT NOT NULL,
    reporter_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_reports_spot ON reports(spot_id);

-- One row per (spot, user): re-confirming refreshes created_at instead of
-- piling up rows, so count = distinct users and spam by a single account is
-- bounded structurally.
CREATE TABLE IF NOT EXISTS confirmations (
    spot_id    TEXT NOT NULL REFERENCES spots(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (spot_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_confirmations_spot_time ON confirmations(spot_id, created_at);
