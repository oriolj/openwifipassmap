// Package store is the SQLite data layer, shared (in part) by server and CLI.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oriolj/openwifipassmap/internal/geo"
	"github.com/oriolj/openwifipassmap/internal/models"

	_ "modernc.org/sqlite"
)

// GeohashPrecision is the character precision used for spot geohashes (~1.2 km).
const GeohashPrecision = 6

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrSelfConfirm is returned when a user tries to confirm their own spot.
// The spot's creator has already attested by adding it; counting their click
// would inflate the signal and let single-user spots fake validation.
var ErrSelfConfirm = errors.New("store: cannot confirm your own spot")

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

// ensureColumn adds a column to a table via ALTER when it's missing — SQLite
// has no ADD COLUMN IF NOT EXISTS, so it probes table_info first. columnDDL is
// the full column definition (e.g. "email TEXT NOT NULL DEFAULT ”"). table and
// column are caller-supplied literals, never user input. Idempotent.
func (s *Store) ensureColumn(ctx context.Context, table, column, columnDDL string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			has = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !has {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+columnDDL); err != nil {
			return err
		}
	}
	return nil
}

// EnsureUserEmail brings a pre-existing database up to the email-bearing users
// schema: it adds the users.email and email_verified columns when missing and
// backfills any account that predates them with the given address. Fresh
// databases already have the columns from schema.sql and only the (no-op)
// backfill runs. Idempotent.
func (s *Store) EnsureUserEmail(ctx context.Context, backfill string) error {
	if err := s.ensureColumn(ctx, "users", "email", `email TEXT NOT NULL DEFAULT '' COLLATE NOCASE`); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "users", "email_verified", `email_verified INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	// Index lives here (not schema.sql) so it's created only once the column is
	// guaranteed present — schema.sql runs before this on a pre-email DB.
	if _, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`); err != nil {
		return err
	}
	if backfill != "" {
		// Only blank emails — i.e. rows that existed before the column — are
		// touched; real registrations always write a non-empty address. The
		// backfill address is the operator's own, so mark it verified or the
		// pre-existing accounts could never use password reset.
		if _, err := s.db.ExecContext(ctx,
			`UPDATE users SET email = ?, email_verified = 1, updated_at = ? WHERE email = ''`, backfill, nowMS()); err != nil {
			return err
		}
	}
	return nil
}

// EnsureSpotQuality adds the spots.quality column (0 = unrated) to a database
// that predates it. Fresh databases already have it from schema.sql. Idempotent.
func (s *Store) EnsureSpotQuality(ctx context.Context) error {
	return s.ensureColumn(ctx, "spots", "quality", `quality INTEGER NOT NULL DEFAULT 0`)
}

func nowMS() int64 { return time.Now().UnixMilli() }

// ---- Users ----

// ErrUsernameTaken is returned when a username already exists.
var ErrUsernameTaken = errors.New("store: username already taken")

// userColumns is the canonical SELECT column order for a user row, kept in
// lock-step with scanUserRow.
const userColumns = `id, username, email, email_verified, password_hash, is_admin, created_at, updated_at`

// scanUserRow reads one user row in userColumns order, translating
// sql.ErrNoRows to ErrNotFound. Works for both *sql.Row and *sql.Rows.
func scanUserRow(sc models.Scanner) (*models.User, error) {
	var u models.User
	err := sc.Scan(&u.ID, &u.Username, &u.Email, &u.EmailVerified, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser inserts a new user. passwordHash must already be hashed; email may
// be empty only for legacy/internal callers (the API requires a valid address).
func (s *Store) CreateUser(ctx context.Context, username, email, passwordHash string) (*models.User, error) {
	u := &models.User{
		ID:           models.NewID(),
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
		CreatedAt:    nowMS(),
	}
	u.UpdatedAt = u.CreatedAt
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?)`,
		u.ID, u.Username, u.Email, u.PasswordHash, u.CreatedAt, u.UpdatedAt)
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
	return scanUserRow(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = ? COLLATE NOCASE`, username))
}

// GetUserByID looks up a user by id.
func (s *Store) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	return scanUserRow(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// GetUsersByEmail returns every account registered under email (case-insensitive).
// Email is intentionally not unique, so this can return more than one user; the
// forgot-password flow issues a reset link for each.
func (s *Store) GetUsersByEmail(ctx context.Context, email string) ([]*models.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ? COLLATE NOCASE ORDER BY created_at`, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUserPassword replaces a user's password hash (used by password reset).
func (s *Store) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, nowMS(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
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
	return scanUserRow(s.db.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.email, u.email_verified, u.password_hash, u.is_admin, u.created_at, u.updated_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token = ? AND s.expires_at > ?`, token, nowMS()))
}

// DeleteSession removes a session token (logout).
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// DeleteUserSessions revokes every session for a user (used after a password
// reset so any stolen/old token stops working).
func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// ---- Password reset tokens ----

// CreatePasswordResetToken issues a single-use reset token for userID, valid
// for ttl. Returns the raw token to embed in the magic link.
func (s *Store) CreatePasswordResetToken(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	token := models.NewToken()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		token, userID, nowMS(), time.Now().Add(ttl).UnixMilli())
	if err != nil {
		return "", err
	}
	return token, nil
}

// ConsumePasswordResetToken atomically validates and burns a reset token,
// returning the user it belongs to. The DELETE ... RETURNING makes use
// single-shot even under concurrent submits. Returns ErrNotFound when the
// token is unknown or expired.
func (s *Store) ConsumePasswordResetToken(ctx context.Context, token string) (string, error) {
	var userID string
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM password_reset_tokens WHERE token = ? AND expires_at > ? RETURNING user_id`,
		token, nowMS()).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return userID, nil
}

// ---- Email verification tokens ----

// CreateEmailVerificationToken issues a single-use verification token for
// userID, valid for ttl. Returns the raw token for the verification link.
func (s *Store) CreateEmailVerificationToken(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	token := models.NewToken()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO email_verification_tokens (token, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		token, userID, nowMS(), time.Now().Add(ttl).UnixMilli())
	if err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeEmailVerificationToken burns a verification token and marks its
// user's email verified, returning the user id. ErrNotFound when the token is
// unknown or expired.
func (s *Store) ConsumeEmailVerificationToken(ctx context.Context, token string) (string, error) {
	var userID string
	err := s.db.QueryRowContext(ctx,
		`DELETE FROM email_verification_tokens WHERE token = ? AND expires_at > ? RETURNING user_id`,
		token, nowMS()).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE users SET email_verified = 1, updated_at = ? WHERE id = ?`, nowMS(), userID)
	return userID, err
}

// ---- Spots ----

// CreateSpot inserts a spot, computing its geohash. ID/timestamps are set here.
//
// It is idempotent per owner: if the same user already has a spot with the same
// SSID at the same coordinates, the existing spot is returned instead of a
// duplicate being inserted (so re-running an import doesn't pile up copies).
// Scoped to created_by so one user can never overwrite or shadow another's spot.
func (s *Store) CreateSpot(ctx context.Context, sp *models.Spot) (*models.Spot, error) {
	if existing, err := s.ownedSpot(ctx, sp.CreatedBy, sp.ESSID, sp.Lat, sp.Lng); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	sp.ID = models.NewID()
	sp.CreatedAt = nowMS()
	sp.UpdatedAt = sp.CreatedAt
	sp.Geohash = geo.Encode(sp.Lat, sp.Lng, GeohashPrecision)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO spots (id, venue_name, essid, password, auth_type, lat, lng, geohash,
		 notes, ping_ms, down_mbps, up_mbps, quality, created_by, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sp.ID, sp.VenueName, sp.ESSID, sp.Password, sp.AuthType, sp.Lat, sp.Lng, sp.Geohash,
		sp.Notes, sp.PingMS, sp.DownMbps, sp.UpMbps, sp.Quality, sp.CreatedBy, sp.CreatedAt, sp.UpdatedAt)
	if err != nil {
		return nil, err
	}
	// The creator's initial rating/speed is their review, so community
	// aggregates include it (the spot row already carries the same values).
	if sp.Quality > 0 || sp.DownMbps != nil || sp.UpMbps != nil || sp.PingMS != nil {
		if _, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO reviews (spot_id, user_id, quality, down_mbps, up_mbps, ping_ms, created_at, updated_at)
			 VALUES (?,?,?,?,?,?,?,?)`,
			sp.ID, sp.CreatedBy, sp.Quality, sp.DownMbps, sp.UpMbps, sp.PingMS, sp.CreatedAt, sp.CreatedAt); err != nil {
			return nil, err
		}
		// Keep the create response's aggregates accurate without a re-read.
		if sp.Quality > 0 {
			sp.RatingsCount = 1
		}
		sp.MyRating = sp.Quality
	}
	return sp, nil
}

// GetSpot fetches a spot by id, enriched with confirmation + review stats.
// viewerID is the requesting user's id (empty for anonymous) and powers
// ConfirmedByMe / MyRating.
func (s *Store) GetSpot(ctx context.Context, id, viewerID string) (*models.Spot, error) {
	sp, err := scanSpot(s.db.QueryRowContext(ctx, spotSelect+` WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := s.enrichSpots(ctx, viewerID, []*models.Spot{sp}); err != nil {
		return nil, err
	}
	return sp, nil
}

// ownedSpot returns an existing spot owned by createdBy with the same SSID and
// exact coordinates, or ErrNotFound. Used for create idempotency.
func (s *Store) ownedSpot(ctx context.Context, createdBy, essid string, lat, lng float64) (*models.Spot, error) {
	return scanSpot(s.db.QueryRowContext(ctx,
		spotSelect+` WHERE created_by = ? AND essid = ? AND lat = ? AND lng = ? LIMIT 1`,
		createdBy, essid, lat, lng))
}

// UpdateSpot updates the factual fields of a spot (venue, network, location,
// notes — recomputing geohash). Quality and speed are NOT written here: they
// are community aggregates owned by the reviews table (see UpsertReview).
func (s *Store) UpdateSpot(ctx context.Context, sp *models.Spot) error {
	sp.UpdatedAt = nowMS()
	sp.Geohash = geo.Encode(sp.Lat, sp.Lng, GeohashPrecision)
	res, err := s.db.ExecContext(ctx,
		`UPDATE spots SET venue_name=?, essid=?, password=?, auth_type=?, lat=?, lng=?,
		 geohash=?, notes=?, updated_at=? WHERE id=?`,
		sp.VenueName, sp.ESSID, sp.Password, sp.AuthType, sp.Lat, sp.Lng, sp.Geohash,
		sp.Notes, sp.UpdatedAt, sp.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountSpotsByUser returns how many spots a user has contributed. Backed by
// idx_spots_created_by, so it's a cheap index scan, not a table scan.
func (s *Store) CountSpotsByUser(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM spots WHERE created_by = ?`, userID).Scan(&n)
	return n, err
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

const spotSelect = `SELECT ` + models.SpotColumns + ` FROM spots`

// scanSpot reads one spot row, translating sql.ErrNoRows to ErrNotFound. It
// accepts both *sql.Row (single-row queries) and *sql.Rows (iteration).
func scanSpot(sc models.Scanner) (*models.Spot, error) {
	sp, err := models.ScanSpot(sc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sp, err
}

// Nearby returns spots within radiusKM of (lat,lng), sorted by distance, capped
// at limit. It bounding-box prefilters in SQL then trims to the true circle.
// The bool reports whether results were actually truncated by the cap (so the
// caller can signal "capped" precisely, not by guessing from len == limit).
// viewerID is the requesting user's id (empty for anonymous) for ConfirmedByMe.
func (s *Store) Nearby(ctx context.Context, lat, lng, radiusKM float64, limit int, viewerID string) ([]*models.Spot, bool, error) {
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
		sp, err := scanSpot(rows)
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
	models.SortByDistance(out)
	truncated := limit > 0 && len(out) > limit
	if truncated {
		out = out[:limit]
	}
	if err := s.enrichSpots(ctx, viewerID, out); err != nil {
		return nil, false, err
	}
	return out, truncated, nil
}

// Area returns a page of spots inside the bounding box of (lat,lng,radiusKM),
// ordered by id for stable cursor pagination. cursor is the last id seen (""
// for the first page). It returns the page and the next cursor ("" when done).
// viewerID is the requesting user's id (empty for anonymous) for ConfirmedByMe.
func (s *Store) Area(ctx context.Context, lat, lng, radiusKM float64, cursor string, limit int, viewerID string) ([]*models.Spot, string, error) {
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
		sp, err := scanSpot(rows)
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
	if err := s.enrichSpots(ctx, viewerID, page); err != nil {
		return nil, "", err
	}
	return page, next, nil
}

// CreateConfirmation records that userID confirmed spotID still works. It is
// an upsert keyed on (spot_id, user_id): re-confirming the same spot refreshes
// created_at instead of inserting a duplicate row, so the count stays equal to
// the number of distinct users. Returns ErrSelfConfirm when the user owns the
// spot, ErrNotFound when the spot doesn't exist.
func (s *Store) CreateConfirmation(ctx context.Context, spotID, userID string) error {
	var createdBy string
	err := s.db.QueryRowContext(ctx, `SELECT created_by FROM spots WHERE id = ?`, spotID).Scan(&createdBy)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if createdBy == userID {
		return ErrSelfConfirm
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO confirmations (spot_id, user_id, created_at) VALUES (?, ?, ?)
		 ON CONFLICT(spot_id, user_id) DO UPDATE SET created_at = excluded.created_at`,
		spotID, userID, nowMS())
	return err
}

// loadConfirmationStats enriches spots in place with LastConfirmedAt,
// ConfirmationsCount and (when viewerID != "") ConfirmedByMe. Single grouped
// query so a 200-spot Nearby response stays one round-trip, not 201.
func (s *Store) loadConfirmationStats(ctx context.Context, viewerID string, spots []*models.Spot) error {
	if len(spots) == 0 {
		return nil
	}
	byID := make(map[string]*models.Spot, len(spots))
	args := make([]any, 0, len(spots)+1)
	args = append(args, viewerID) // for the CASE WHEN match — "" never matches a real uuid
	for _, sp := range spots {
		byID[sp.ID] = sp
		args = append(args, sp.ID)
	}
	placeholders := strings.Repeat("?,", len(spots)-1) + "?"
	rows, err := s.db.QueryContext(ctx, `SELECT spot_id, MAX(created_at), COUNT(*),
		MAX(CASE WHEN user_id = ? THEN 1 ELSE 0 END)
		FROM confirmations WHERE spot_id IN (`+placeholders+`) GROUP BY spot_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var spotID string
		var lastAt int64
		var count, byMe int
		if err := rows.Scan(&spotID, &lastAt, &count, &byMe); err != nil {
			return err
		}
		if sp := byID[spotID]; sp != nil {
			at := lastAt
			sp.LastConfirmedAt = &at
			sp.ConfirmationsCount = count
			sp.ConfirmedByMe = byMe == 1
		}
	}
	return rows.Err()
}

// enrichSpots loads all read-time aggregates (confirmation + review stats)
// onto spots in place. Every spot-returning query path goes through this.
func (s *Store) enrichSpots(ctx context.Context, viewerID string, spots []*models.Spot) error {
	if err := s.loadConfirmationStats(ctx, viewerID, spots); err != nil {
		return err
	}
	return s.loadReviewStats(ctx, viewerID, spots)
}

// loadReviewStats enriches spots in place with RatingsCount and (when
// viewerID != "") MyRating. Same single-grouped-query shape as
// loadConfirmationStats.
func (s *Store) loadReviewStats(ctx context.Context, viewerID string, spots []*models.Spot) error {
	if len(spots) == 0 {
		return nil
	}
	byID := make(map[string]*models.Spot, len(spots))
	args := make([]any, 0, len(spots)+1)
	args = append(args, viewerID)
	for _, sp := range spots {
		byID[sp.ID] = sp
		args = append(args, sp.ID)
	}
	placeholders := strings.Repeat("?,", len(spots)-1) + "?"
	rows, err := s.db.QueryContext(ctx, `SELECT spot_id,
		COUNT(CASE WHEN quality > 0 THEN 1 END),
		COALESCE(MAX(CASE WHEN user_id = ? THEN quality END), 0)
		FROM reviews WHERE spot_id IN (`+placeholders+`) GROUP BY spot_id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var spotID string
		var count, mine int
		if err := rows.Scan(&spotID, &count, &mine); err != nil {
			return err
		}
		if sp := byID[spotID]; sp != nil {
			sp.RatingsCount = count
			sp.MyRating = mine
		}
	}
	return rows.Err()
}

// UpsertReview records (or updates) userID's review of spotID — a quality
// rating (1-3) and/or a speed measurement — then recomputes the spot's cached
// aggregates: quality = rounded average of all ratings, speed fields = the
// most recent measurement. Nil inputs leave the user's previous values in
// place, so a speed-only re-review doesn't erase an earlier rating. Returns
// ErrNotFound when the spot doesn't exist. Any user may review any spot,
// including their own (that's just editing their initial rating).
func (s *Store) UpsertReview(ctx context.Context, spotID, userID string, quality *int, down, up *float64, ping *int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM spots WHERE id = ?`, spotID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	now := nowMS()
	q := 0
	if quality != nil {
		q = *quality
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO reviews (spot_id, user_id, quality, down_mbps, up_mbps, ping_ms, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(spot_id, user_id) DO UPDATE SET
		   quality   = CASE WHEN excluded.quality > 0 THEN excluded.quality ELSE reviews.quality END,
		   down_mbps = COALESCE(excluded.down_mbps, reviews.down_mbps),
		   up_mbps   = COALESCE(excluded.up_mbps, reviews.up_mbps),
		   ping_ms   = COALESCE(excluded.ping_ms, reviews.ping_ms),
		   updated_at = excluded.updated_at`,
		spotID, userID, q, down, up, ping, now, now); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE spots SET
		   quality   = COALESCE((SELECT CAST(ROUND(AVG(quality)) AS INTEGER) FROM reviews WHERE spot_id = ?1 AND quality > 0), 0),
		   down_mbps = (SELECT down_mbps FROM reviews WHERE spot_id = ?1 AND down_mbps IS NOT NULL ORDER BY updated_at DESC, rowid DESC LIMIT 1),
		   up_mbps   = (SELECT up_mbps   FROM reviews WHERE spot_id = ?1 AND up_mbps   IS NOT NULL ORDER BY updated_at DESC, rowid DESC LIMIT 1),
		   ping_ms   = (SELECT ping_ms   FROM reviews WHERE spot_id = ?1 AND ping_ms   IS NOT NULL ORDER BY updated_at DESC, rowid DESC LIMIT 1),
		   updated_at = ?2
		 WHERE id = ?1`,
		spotID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// EnsureReviews backfills the reviews table from spots that predate it: each
// spot's owner-set quality/speed becomes the owner's review row. Only spots
// with no reviews at all are touched (a spot that already has community
// reviews must not gain a fabricated owner rating from its aggregate), making
// this idempotent and safe on every boot.
func (s *Store) EnsureReviews(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO reviews (spot_id, user_id, quality, down_mbps, up_mbps, ping_ms, created_at, updated_at)
		 SELECT id, created_by, quality, down_mbps, up_mbps, ping_ms, created_at, updated_at FROM spots
		 WHERE (quality > 0 OR down_mbps IS NOT NULL OR up_mbps IS NOT NULL OR ping_ms IS NOT NULL)
		   AND NOT EXISTS (SELECT 1 FROM reviews r WHERE r.spot_id = spots.id)`)
	return err
}

// ListReports returns the newest moderation reports with spot context, capped
// at limit, plus the total count so callers can surface truncation explicitly
// (never silently). Reports whose spot was deleted are cascade-removed, so the
// join is always satisfiable.
func (s *Store) ListReports(ctx context.Context, limit int) ([]*models.Report, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.spot_id, r.reason, COALESCE(r.reporter_user_id, ''), r.created_at,
		        s.essid, s.venue_name
		 FROM reports r JOIN spots s ON s.id = r.spot_id
		 ORDER BY r.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*models.Report
	for rows.Next() {
		var rep models.Report
		if err := rows.Scan(&rep.ID, &rep.SpotID, &rep.Reason, &rep.ReporterUserID,
			&rep.CreatedAt, &rep.SpotESSID, &rep.SpotVenueName); err != nil {
			return nil, 0, err
		}
		out = append(out, &rep)
	}
	return out, total, rows.Err()
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
