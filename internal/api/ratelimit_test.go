package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterBurstAndRefill(t *testing.T) {
	rl := newRateLimiter(3, 50*time.Millisecond)

	for i := 0; i < 3; i++ {
		if !rl.allow("a") {
			t.Fatalf("request %d within burst should pass", i+1)
		}
	}
	if rl.allow("a") {
		t.Error("4th immediate request should be limited")
	}
	// Independent keys don't share buckets.
	if !rl.allow("b") {
		t.Error("different key should have its own bucket")
	}
	// After one refill interval, one more token is available.
	time.Sleep(60 * time.Millisecond)
	if !rl.allow("a") {
		t.Error("request after refill should pass")
	}
	if rl.allow("a") {
		t.Error("tokens should not accumulate past the refill earned")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "10.0.0.7:4242"
	if got := clientIP(r); got != "10.0.0.7" {
		t.Errorf("RemoteAddr ip = %q, want 10.0.0.7", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("XFF ip = %q, want first entry 203.0.113.9", got)
	}
}
