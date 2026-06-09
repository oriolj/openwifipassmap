// Package cache is the CLI's local, offline SQLite cache of downloaded public
// spots. It uses a standalone schema (no user/session tables, no foreign keys)
// so spots downloaded from the server can be stored verbatim by id.
package cache

import (
	"context"
	"database/sql"
	"strings"

	"github.com/oriolj/openwifipassmap/internal/geo"
	"github.com/oriolj/openwifipassmap/internal/models"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS spots (
    id TEXT PRIMARY KEY, venue_name TEXT, essid TEXT, password TEXT, auth_type TEXT,
    lat REAL, lng REAL, geohash TEXT, notes TEXT,
    ping_ms INTEGER, down_mbps REAL, up_mbps REAL, quality INTEGER NOT NULL DEFAULT 0,
    created_by TEXT, created_at INTEGER, updated_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_cache_latlng ON spots(lat, lng);
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
`

// Cache is the on-disk spot cache.
type Cache struct{ db *sql.DB }

// Open opens (creating if needed) the cache at path.
func Open(path string) (*Cache, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		return nil, err
	}
	// Best-effort upgrade of a cache.db created before `quality` existed. SQLite
	// has no ADD COLUMN IF NOT EXISTS, so ignore the duplicate-column error.
	if _, err := db.ExecContext(context.Background(),
		`ALTER TABLE spots ADD COLUMN quality INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column name") {
		return nil, err
	}
	return &Cache{db: db}, nil
}

// Close closes the cache.
func (c *Cache) Close() error { return c.db.Close() }

// Upsert inserts or replaces a spot by id.
func (c *Cache) Upsert(ctx context.Context, sp *models.Spot) error {
	_, err := c.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO spots
		 (id, venue_name, essid, password, auth_type, lat, lng, geohash, notes,
		  ping_ms, down_mbps, up_mbps, quality, created_by, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sp.ID, sp.VenueName, sp.ESSID, sp.Password, sp.AuthType, sp.Lat, sp.Lng,
		sp.Geohash, sp.Notes, sp.PingMS, sp.DownMbps, sp.UpMbps, sp.Quality, sp.CreatedBy,
		sp.CreatedAt, sp.UpdatedAt)
	return err
}

// Count returns the number of cached spots.
func (c *Cache) Count(ctx context.Context) (int, error) {
	var n int
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM spots`).Scan(&n)
	return n, err
}

// SetMeta stores a small key/value (e.g. last sync coordinates).
func (c *Cache) SetMeta(ctx context.Context, key, value string) error {
	_, err := c.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`, key, value)
	return err
}

// Nearby returns cached spots within radiusKM of (lat,lng), sorted by distance.
func (c *Cache) Nearby(ctx context.Context, lat, lng, radiusKM float64) ([]*models.Spot, error) {
	minLat, maxLat, minLng, maxLng := geo.BoundingBox(lat, lng, radiusKM)
	rows, err := c.db.QueryContext(ctx,
		`SELECT `+models.SpotColumns+`
		 FROM spots WHERE lat BETWEEN ? AND ? AND lng BETWEEN ? AND ?`,
		minLat, maxLat, minLng, maxLng)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*models.Spot
	for rows.Next() {
		sp, err := models.ScanSpot(rows)
		if err != nil {
			return nil, err
		}
		d := geo.HaversineKM(lat, lng, sp.Lat, sp.Lng)
		if d <= radiusKM {
			sp.DistanceKM = &d
			out = append(out, sp)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	models.SortByDistance(out)
	return out, nil
}
