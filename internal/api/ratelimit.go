package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket: each key gets `burst` tokens that
// refill at one token per `refill`. Buckets idle long enough to be full again
// are pruned opportunistically so the map can't grow without bound.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	burst   float64
	refill  time.Duration
	// lastPrune gates the sweep so it runs at most once per prune interval.
	lastPrune time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(burst int, refill time.Duration) *rateLimiter {
	return &rateLimiter{
		buckets:   make(map[string]*bucket),
		burst:     float64(burst),
		refill:    refill,
		lastPrune: time.Now(),
	}
}

// allow reports whether key may proceed, consuming one token if so.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()

	// Opportunistic prune: drop buckets that have fully refilled (they're
	// indistinguishable from absent ones). Runs at most once per 10×refill.
	if now.Sub(rl.lastPrune) > 10*rl.refill {
		for k, b := range rl.buckets {
			if now.Sub(b.last) >= time.Duration(rl.burst)*rl.refill {
				delete(rl.buckets, k)
			}
		}
		rl.lastPrune = now
	}

	b, ok := rl.buckets[key]
	if !ok {
		rl.buckets[key] = &bucket{tokens: rl.burst - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() / rl.refill.Seconds()
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// clientIP extracts the caller's IP for rate-limit keying. Behind Coolify's
// Traefik the real client is the first X-Forwarded-For entry; locally it's
// RemoteAddr. (A direct-to-origin caller could spoof the header, but the
// limiter is an abuse brake, not an auth boundary.)
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimit wraps a handler, answering 429 once the caller's bucket is empty.
func (a *API) rateLimit(rl *rateLimiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow(clientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, "too many requests — try again later")
			return
		}
		next(w, r)
	}
}
