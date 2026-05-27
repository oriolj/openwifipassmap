// Package geo holds proximity helpers shared by the server and CLI:
// haversine distance, a bounding box for radius prefiltering, and geohashing.
package geo

import (
	"math"

	"github.com/mmcloughlin/geohash"
)

const earthRadiusKM = 6371.0

// HaversineKM returns the great-circle distance in kilometres between two
// lat/lng points.
func HaversineKM(lat1, lng1, lat2, lng2 float64) float64 {
	rlat1 := lat1 * math.Pi / 180
	rlat2 := lat2 * math.Pi / 180
	dlat := (lat2 - lat1) * math.Pi / 180
	dlng := (lng2 - lng1) * math.Pi / 180

	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(rlat1)*math.Cos(rlat2)*math.Sin(dlng/2)*math.Sin(dlng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKM * c
}

// BoundingBox returns lat/lng bounds that fully contain a circle of radiusKM
// centred on (lat, lng). It is a cheap SQL prefilter; callers must still apply
// HaversineKM to trim the box corners down to the true circle.
func BoundingBox(lat, lng, radiusKM float64) (minLat, maxLat, minLng, maxLng float64) {
	latDelta := radiusKM / 111.0 // ~111 km per degree of latitude
	// Degrees of longitude per km grows toward the poles; guard cos→0.
	cos := math.Cos(lat * math.Pi / 180)
	if cos < 1e-6 {
		cos = 1e-6
	}
	lngDelta := radiusKM / (111.0 * cos)

	minLat = clampLat(lat - latDelta)
	maxLat = clampLat(lat + latDelta)
	minLng = lng - lngDelta
	maxLng = lng + lngDelta
	// Clamp longitude span to a full circle to avoid nonsense bounds for huge radii.
	if maxLng-minLng >= 360 {
		minLng, maxLng = -180, 180
	}
	return
}

func clampLat(v float64) float64 {
	if v > 90 {
		return 90
	}
	if v < -90 {
		return -90
	}
	return v
}

// Encode returns the geohash of a point at the given character precision.
func Encode(lat, lng float64, precision uint) string {
	return geohash.EncodeWithPrecision(lat, lng, precision)
}
