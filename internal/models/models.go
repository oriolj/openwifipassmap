// Package models holds the core data types shared across the server and CLI.
package models

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// User is a contributor. Browsing is anonymous; an account is only needed to
// add/edit/delete spots.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
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

// NewToken returns a 32-byte random token, hex-encoded.
func NewToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("models: cannot read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
