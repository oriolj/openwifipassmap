package geo

import (
	"math"
	"testing"
)

func TestHaversineKM(t *testing.T) {
	// Barcelona ↔ Paris is ~830 km.
	d := HaversineKM(41.3851, 2.1734, 48.8566, 2.3522)
	if math.Abs(d-830) > 30 {
		t.Fatalf("expected ~830 km, got %.1f", d)
	}
	// Same point is 0.
	if d0 := HaversineKM(41.3851, 2.1734, 41.3851, 2.1734); d0 > 1e-9 {
		t.Fatalf("expected 0 for identical points, got %v", d0)
	}
}

func TestBoundingBoxContainsCircle(t *testing.T) {
	lat, lng, r := 41.3851, 2.1734, 10.0
	minLat, maxLat, minLng, maxLng := BoundingBox(lat, lng, r)
	if lat < minLat || lat > maxLat || lng < minLng || lng > maxLng {
		t.Fatal("center must be inside its own bounding box")
	}
	// A point ~9 km due north (well inside the radius) must be inside the box.
	north := lat + 9.0/111.0
	if north > maxLat {
		t.Fatalf("a point 9km north (%.4f) should be within maxLat (%.4f)", north, maxLat)
	}
}

func TestEncodePrecision(t *testing.T) {
	gh := Encode(41.3851, 2.1734, 6)
	if len(gh) != 6 {
		t.Fatalf("expected 6-char geohash, got %q (len %d)", gh, len(gh))
	}
}
