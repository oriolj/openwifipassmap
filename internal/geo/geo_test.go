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

func TestBoundingBoxAntimeridian(t *testing.T) {
	// Near the date line, the box must widen to the full longitude range so the
	// SQL prefilter doesn't miss spots just across ±180°.
	minLat, maxLat, minLng, maxLng := BoundingBox(0, 179.5, 100)
	if minLng != -180 || maxLng != 180 {
		t.Fatalf("expected full longitude range near antimeridian, got [%v, %v]", minLng, maxLng)
	}
	// A spot ~10 km across the date line (lng = -179.6) must fall within the box.
	spotLng := -179.6
	if spotLng < minLng || spotLng > maxLng {
		t.Fatalf("spot across the date line (%v) should be inside the box", spotLng)
	}
	if minLat >= maxLat {
		t.Fatal("latitude band should still be a valid range")
	}
}

func TestEncodePrecision(t *testing.T) {
	gh := Encode(41.3851, 2.1734, 6)
	if len(gh) != 6 {
		t.Fatalf("expected 6-char geohash, got %q (len %d)", gh, len(gh))
	}
}
