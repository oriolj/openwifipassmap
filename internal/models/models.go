// Package models holds the core data types shared across the server and CLI.
package models

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
)

// User is a contributor. Browsing is anonymous; an account is only needed to
// add/edit/delete spots.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	IsAdmin      bool   `json:"is_admin"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// Session is an opaque bearer token tied to a user.
type Session struct {
	Token     string `json:"token"`
	UserID    string `json:"user_id"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// Spot is a public WiFi location. Everything here is public by design.
type Spot struct {
	ID        string   `json:"id"`
	VenueName string   `json:"venue_name"`
	ESSID     string   `json:"essid"`
	Password  string   `json:"password"`  // empty for open networks
	AuthType  string   `json:"auth_type"` // wpa2 | wpa3 | wep | open
	Lat       float64  `json:"lat"`
	Lng       float64  `json:"lng"`
	Geohash   string   `json:"geohash"`
	Notes     string   `json:"notes"`
	PingMS    *int     `json:"ping_ms,omitempty"`
	DownMbps  *float64 `json:"down_mbps,omitempty"`
	UpMbps    *float64 `json:"up_mbps,omitempty"`
	CreatedBy string   `json:"created_by"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`

	// DistanceKM is populated only by proximity queries; not stored.
	DistanceKM *float64 `json:"distance_km,omitempty"`

	// Confirmation aggregates (populated by the store on read; not stored on
	// the spot row itself). LastConfirmedAt is nil when nobody has confirmed,
	// ConfirmationsCount is always present (0 when none), and ConfirmedByMe is
	// only true when the request is authenticated and the caller has confirmed.
	LastConfirmedAt    *int64 `json:"last_confirmed_at,omitempty"`
	ConfirmationsCount int    `json:"confirmations_count"`
	ConfirmedByMe      bool   `json:"confirmed_by_me,omitempty"`
}

// Confirmation is a user attesting that a spot's credentials work. One row per
// (spot, user) — re-confirming refreshes created_at.
type Confirmation struct {
	SpotID    string `json:"spot_id"`
	UserID    string `json:"user_id"`
	CreatedAt int64  `json:"created_at"`
}

// Report is a lightweight moderation signal.
type Report struct {
	ID             string `json:"id"`
	SpotID         string `json:"spot_id"`
	Reason         string `json:"reason"` // wrong_password | gone | spam | other
	ReporterUserID string `json:"reporter_user_id,omitempty"`
	CreatedAt      int64  `json:"created_at"`
}

// ValidAuthTypes is the allowed set for Spot.AuthType.
var ValidAuthTypes = map[string]bool{"wpa2": true, "wpa3": true, "wep": true, "open": true}

// ValidReportReasons is the allowed set for Report.Reason.
var ValidReportReasons = map[string]bool{"wrong_password": true, "gone": true, "spam": true, "other": true}

// NewID returns a random RFC-4122 v4 UUID string, avoiding an external dep.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("models: cannot read random bytes: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// SpotColumns is the canonical SELECT column order for a spot row, shared by
// the server store and the CLI cache so both stay in lock-step with ScanSpot.
const SpotColumns = `id, venue_name, essid, password, auth_type, lat, lng, geohash, notes,
	ping_ms, down_mbps, up_mbps, created_by, created_at, updated_at`

// Scanner is the read side shared by *sql.Row and *sql.Rows, letting the store
// and the CLI cache reuse one spot-row scanner.
type Scanner interface{ Scan(dst ...any) error }

// ScanSpot reads one spot row from sc in SpotColumns order, mapping the three
// nullable speed columns onto their pointer fields. It returns the raw scan
// error (including sql.ErrNoRows) for the caller to interpret.
func ScanSpot(sc Scanner) (*Spot, error) {
	var sp Spot
	var ping sql.NullInt64
	var down, up sql.NullFloat64
	if err := sc.Scan(&sp.ID, &sp.VenueName, &sp.ESSID, &sp.Password, &sp.AuthType,
		&sp.Lat, &sp.Lng, &sp.Geohash, &sp.Notes, &ping, &down, &up,
		&sp.CreatedBy, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
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

// SortByDistance sorts spots ascending by DistanceKM (a nil distance sorts as 0).
func SortByDistance(spots []*Spot) {
	sort.Slice(spots, func(i, j int) bool {
		return DerefF64(spots[i].DistanceKM) < DerefF64(spots[j].DistanceKM)
	})
}

// DerefF64 returns *f, or 0 when f is nil.
func DerefF64(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

// NewToken returns a 32-byte random token, hex-encoded.
func NewToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("models: cannot read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
