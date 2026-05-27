// Package store is the SQLite data layer, shared (in part) by server and CLI.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oriolj/wifi_psw_sharer/internal/geo"
	"github.com/oriolj/wifi_psw_sharer/internal/models"

	_ "modernc.org/sqlite"
)

// GeohashPrecision is the character precision used for spot geohashes (~1.2 km).
const GeohashPrecision = 6

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store wraps a SQLite database with production-friendly PRAGMAs.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) a SQLite database at path and applies the
// recommended production PRAGMAs (WAL, busy_timeout, NORMAL sync, FKs).
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"+
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"+
		"&_pragma=cache_size(-64000)&_pragma=temp_store(MEMORY)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	// SQLite allows a single writer; cap the pool at one connection so writes
	// serialize cleanly instead of racing for the WAL lock and hitting
	// SQLITE_BUSY. Reads are sub-millisecond at this scale; if read throughput
	// ever matters, split into a separate read-only *sql.DB pool.
	db.SetMaxOpenConns(1)
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Migrate applies the given schema SQL (expects IF NOT EXISTS statements).
func (s *Store) Migrate(ctx context.Context, schema string) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func nowMS() int64 { return time.Now().UnixMilli() }

// ---- Users ----

// ErrUsernameTaken is returned when a username already exists.
var ErrUsernameTaken = errors.New("store: username already taken")

// CreateUser inserts a new user. passwordHash must already be hashed.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (*models.User, error) {
	u := &models.User{
		ID:           models.NewID(),
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    nowMS(),
	}
	u.UpdatedAt = u.CreatedAt
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		// Only a UNIQUE violation means the username is taken; surface every
		// other error (locked DB, disk full, …) as itself so it isn't masked
		// as a 409 and is logged by the caller.
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	return u, nil
}

// GetUserByUsername looks up a user by (case-insensitive) username.
func (s *Store) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, is_admin, created_at, updated_at
		 FROM users WHERE username = ? COLLATE NOCASE`, username))
}

// GetUserByID looks up a user by id.
func (s *Store) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, is_admin, created_at, updated_at
		 FROM users WHERE id = ?`, id))
}

func (s *Store) scanUser(row *sql.Row) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ---- Sessions ----

// CreateSession creates a bearer-token session valid for ttl.
func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration) (*models.Session, error) {
	sess := &models.Session{
		Token:     models.NewToken(),
		UserID:    userID,
		CreatedAt: nowMS(),
		ExpiresAt: time.Now().Add(ttl).UnixMilli(),
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		sess.Token, sess.UserID, sess.CreatedAt, sess.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// UserForToken returns the user owning a non-expired session token.
func (s *Store) UserForToken(ctx context.Context, token string) (*models.User, error) {
	var u models.User
	err := s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.password_hash, u.is_admin, u.created_at, u.updated_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token = ? AND s.expires_at > ?`, token, nowMS()).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteSession removes a session token (logout).
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// ---- Spots ----

// CreateSpot inserts a spot, computing its geohash. ID/timestamps are set here.
func (s *Store) CreateSpot(ctx context.Context, sp *models.Spot) (*models.Spot, error) {
	sp.ID = models.NewID()
	sp.CreatedAt = nowMS()
	sp.UpdatedAt = sp.CreatedAt
	sp.Geohash = geo.Encode(sp.Lat, sp.Lng, GeohashPrecision)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO spots (id, venue_name, essid, password, auth_type, lat, lng, geohash,
		 notes, ping_ms, down_mbps, up_mbps, created_by, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sp.ID, sp.VenueName, sp.ESSID, sp.Password, sp.AuthType, sp.Lat, sp.Lng, sp.Geohash,
		sp.Notes, sp.PingMS, sp.DownMbps, sp.UpMbps, sp.CreatedBy, sp.CreatedAt, sp.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return sp, nil
}

// GetSpot fetches a spot by id.
func (s *Store) GetSpot(ctx context.Context, id string) (*models.Spot, error) {
	return scanSpot(s.db.QueryRowContext(ctx, spotSelect+` WHERE id = ?`, id))
}

// UpdateSpot updates the mutable fields of a spot (recomputing geohash).
func (s *Store) UpdateSpot(ctx context.Context, sp *models.Spot) error {
	sp.UpdatedAt = nowMS()
	sp.Geohash = geo.Encode(sp.Lat, sp.Lng, GeohashPrecision)
	res, err := s.db.ExecContext(ctx,
		`UPDATE spots SET venue_name=?, essid=?, password=?, auth_type=?, lat=?, lng=?,
		 geohash=?, notes=?, ping_ms=?, down_mbps=?, up_mbps=?, updated_at=? WHERE id=?`,
		sp.VenueName, sp.ESSID, sp.Password, sp.AuthType, sp.Lat, sp.Lng, sp.Geohash,
		sp.Notes, sp.PingMS, sp.DownMbps, sp.UpMbps, sp.UpdatedAt, sp.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSpot removes a spot by id.
func (s *Store) DeleteSpot(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM spots WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const spotSelect = `SELECT id, venue_name, essid, password, auth_type, lat, lng, geohash,
	notes, ping_ms, down_mbps, up_mbps, created_by, created_at, updated_at FROM spots`

func scanSpot(row *sql.Row) (*models.Spot, error) {
	sp, err := scanSpotRows(rowToRows(row))
	if err != nil {
		return nil, err
	}
	return sp, nil
}

// rowToRows is a tiny shim so scanSpotRows can serve both *sql.Row and *sql.Rows.
type singleRow struct{ r *sql.Row }

func rowToRows(r *sql.Row) *singleRow      { return &singleRow{r} }
func (s *singleRow) Scan(dst ...any) error { return s.r.Scan(dst...) }

type scanner interface{ Scan(dst ...any) error }

func scanSpotRows(sc scanner) (*models.Spot, error) {
	var sp models.Spot
	var ping sql.NullInt64
	var down, up sql.NullFloat64
	err := sc.Scan(&sp.ID, &sp.VenueName, &sp.ESSID, &sp.Password, &sp.AuthType,
		&sp.Lat, &sp.Lng, &sp.Geohash, &sp.Notes, &ping, &down, &up,
		&sp.CreatedBy, &sp.CreatedAt, &sp.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ping.Valid {
		v := int(ping.Int64)
		sp.PingMS = &v
	}
	if down.Valid {
		sp.DownMbps = &down.Float64
	}
	if up.Valid {
		sp.UpMbps = &up.Float64
	}
	return &sp, nil
}

// Nearby returns spots within radiusKM of (lat,lng), sorted by distance, capped
// at limit. It bounding-box prefilters in SQL then trims to the true circle.
// The bool reports whether results were actually truncated by the cap (so the
// caller can signal "capped" precisely, not by guessing from len == limit).
func (s *Store) Nearby(ctx context.Context, lat, lng, radiusKM float64, limit int) ([]*models.Spot, bool, error) {
	minLat, maxLat, minLng, maxLng := geo.BoundingBox(lat, lng, radiusKM)
	rows, err := s.db.QueryContext(ctx,
		spotSelect+` WHERE lat BETWEEN ? AND ? AND lng BETWEEN ? AND ?`,
		minLat, maxLat, minLng, maxLng)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []*models.Spot
	for rows.Next() {
		sp, err := scanSpotRows(rows)
		if err != nil {
			return nil, false, err
		}
		d := geo.HaversineKM(lat, lng, sp.Lat, sp.Lng)
		if d <= radiusKM {
			sp.DistanceKM = &d
			out = append(out, sp)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	sortByDistance(out)
	truncated := limit > 0 && len(out) > limit
	if truncated {
		out = out[:limit]
	}
	return out, truncated, nil
}

// Area returns a page of spots inside the bounding box of (lat,lng,radiusKM),
// ordered by id for stable cursor pagination. cursor is the last id seen (""
// for the first page). It returns the page and the next cursor ("" when done).
func (s *Store) Area(ctx context.Context, lat, lng, radiusKM float64, cursor string, limit int) ([]*models.Spot, string, error) {
	minLat, maxLat, minLng, maxLng := geo.BoundingBox(lat, lng, radiusKM)
	rows, err := s.db.QueryContext(ctx,
		spotSelect+` WHERE lat BETWEEN ? AND ? AND lng BETWEEN ? AND ? AND id > ?
		 ORDER BY id LIMIT ?`,
		minLat, maxLat, minLng, maxLng, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var page []*models.Spot
	var lastID string
	scanned := 0
	for rows.Next() {
		sp, err := scanSpotRows(rows)
		if err != nil {
			return nil, "", err
		}
		scanned++
		lastID = sp.ID
		d := geo.HaversineKM(lat, lng, sp.Lat, sp.Lng)
		if d <= radiusKM {
			sp.DistanceKM = &d
			page = append(page, sp)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	// A full window means more rows may remain past lastID; a short read is the
	// last page. Note the page itself may be shorter than scanned because the
	// haversine trim drops bounding-box corners — paginate on scanned, not page.
	next := ""
	if scanned == limit {
		next = lastID
	}
	return page, next, nil
}

// CreateReport inserts a moderation report.
func (s *Store) CreateReport(ctx context.Context, spotID, reason, reporterID string) (*models.Report, error) {
	r := &models.Report{
		ID:             models.NewID(),
		SpotID:         spotID,
		Reason:         reason,
		ReporterUserID: reporterID,
		CreatedAt:      nowMS(),
	}
	var reporter any
	if reporterID != "" {
		reporter = reporterID
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO reports (id, spot_id, reason, reporter_user_id, created_at) VALUES (?,?,?,?,?)`,
		r.ID, r.SpotID, r.Reason, reporter, r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func sortByDistance(spots []*models.Spot) {
	sort.Slice(spots, func(i, j int) bool {
		return deref(spots[i].DistanceKM) < deref(spots[j].DistanceKM)
	})
}

func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
