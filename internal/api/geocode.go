package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oriolj/openwifipassmap/internal/metrics"
)

// geocodeResult is the trimmed shape sent to clients: floats parsed
// server-side so callers don't deal with Nominatim's stringly-typed fields.
type geocodeResult struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Lat         float64   `json:"lat"`
	Lng         float64   `json:"lng"`
	Bbox        []float64 `json:"bbox,omitempty"` // [minLat, maxLat, minLng, maxLng]
}

// nominatimItem is one entry of Nominatim's jsonv2 search response.
type nominatimItem struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Lat         string   `json:"lat"`
	Lon         string   `json:"lon"`
	BoundingBox []string `json:"boundingbox"` // [minlat, maxlat, minlon, maxlon]
}

var errGeocodeBusy = errors.New("geocoder busy")

type geoCacheEntry struct {
	results []geocodeResult
	expires time.Time
}

// geocoder proxies free-form place search to Nominatim, enforcing its usage
// policy (a single identifying User-Agent, at most one outbound request per
// second) from a shared bottleneck so many concurrent API callers can't
// exceed it, plus a short cache so repeated queries cost nothing upstream.
type geocoder struct {
	base   string
	client *http.Client
	minGap time.Duration
	queue  chan struct{} // bounds waiters queued on the throttle

	mu   sync.Mutex // serializes + paces outbound requests
	last time.Time

	cacheMu  sync.Mutex
	cache    map[string]geoCacheEntry
	cacheTTL time.Duration
	cacheCap int
}

func newGeocoder(base string) *geocoder {
	return &geocoder{
		base:     base,
		client:   &http.Client{Timeout: 5 * time.Second},
		minGap:   time.Second,
		queue:    make(chan struct{}, 3),
		cache:    make(map[string]geoCacheEntry),
		cacheTTL: 24 * time.Hour,
		cacheCap: 1000,
	}
}

var langTagRE = regexp.MustCompile(`^[A-Za-z-]{1,16}$`)

// acceptLanguage extracts and sanitizes the first tag of the Accept-Language
// header so it's safe to forward to Nominatim and use as a cache key.
func acceptLanguage(r *http.Request) string {
	v := r.Header.Get("Accept-Language")
	if i := strings.IndexAny(v, ",;"); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSpace(v)
	if !langTagRE.MatchString(v) {
		return ""
	}
	return v
}

func (g *geocoder) cacheKey(q, lang string) string {
	return strings.ToLower(q) + "|" + lang
}

func (g *geocoder) cacheGet(key string) ([]geocodeResult, bool) {
	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()
	e, ok := g.cache[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.results, true
}

func (g *geocoder) cacheSet(key string, results []geocodeResult) {
	g.cacheMu.Lock()
	defer g.cacheMu.Unlock()
	if len(g.cache) >= g.cacheCap {
		now := time.Now()
		for k, e := range g.cache {
			if now.After(e.expires) {
				delete(g.cache, k)
			}
		}
	}
	if len(g.cache) < g.cacheCap {
		g.cache[key] = geoCacheEntry{results: results, expires: time.Now().Add(g.cacheTTL)}
	}
}

// search returns up to 5 matches for q, from cache when available. It
// serializes and paces outbound Nominatim requests to at most one per
// minGap, and returns errGeocodeBusy if too many callers are already
// waiting on that throttle.
func (g *geocoder) search(ctx context.Context, q, lang string) ([]geocodeResult, error) {
	key := g.cacheKey(q, lang)
	if results, ok := g.cacheGet(key); ok {
		metrics.Geocode.WithLabelValues("cache").Inc()
		return results, nil
	}

	select {
	case g.queue <- struct{}{}:
	default:
		metrics.Geocode.WithLabelValues("busy").Inc()
		return nil, errGeocodeBusy
	}
	defer func() { <-g.queue }()

	g.mu.Lock()
	defer g.mu.Unlock()

	// Re-check the cache: another waiter may have just populated it.
	if results, ok := g.cacheGet(key); ok {
		metrics.Geocode.WithLabelValues("cache").Inc()
		return results, nil
	}

	if wait := g.minGap - time.Since(g.last); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		}
	}

	results, err := g.fetch(ctx, q, lang)
	g.last = time.Now()
	if err != nil {
		metrics.Geocode.WithLabelValues("error").Inc()
		return nil, err
	}
	metrics.Geocode.WithLabelValues("upstream").Inc()
	g.cacheSet(key, results)
	return results, nil
}

func (g *geocoder) fetch(ctx context.Context, q, lang string) ([]geocodeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base, nil)
	if err != nil {
		return nil, err
	}
	query := req.URL.Query()
	query.Set("format", "jsonv2")
	query.Set("q", q)
	query.Set("limit", "5")
	if lang != "" {
		query.Set("accept-language", lang)
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("User-Agent", "OpenWifiPassMap/1.0 (+https://openwifipassmap.oriolj.com; contact: oriolj@gmail.com)")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("nominatim: unexpected status " + resp.Status)
	}

	var items []nominatimItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}

	results := make([]geocodeResult, 0, len(items))
	for _, it := range items {
		lat, err1 := strconv.ParseFloat(it.Lat, 64)
		lon, err2 := strconv.ParseFloat(it.Lon, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		r := geocodeResult{Name: it.Name, DisplayName: it.DisplayName, Lat: lat, Lng: lon}
		if bbox := parseBbox(it.BoundingBox); bbox != nil {
			r.Bbox = bbox
		}
		results = append(results, r)
	}
	return results, nil
}

// parseBbox converts Nominatim's [minlat, maxlat, minlon, maxlon] string
// array to floats, or nil if any entry doesn't parse.
func parseBbox(bbox []string) []float64 {
	if len(bbox) != 4 {
		return nil
	}
	out := make([]float64, 4)
	for i, s := range bbox {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		out[i] = v
	}
	return out
}

// geocode handles GET /api/geocode?q=... — a same-origin proxy to Nominatim
// so the outbound-usage policy (User-Agent, 1 req/s) is enforced in one
// place instead of by every client.
func (a *API) geocode(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q is required")
		return
	}
	if len(q) > 200 {
		writeErr(w, http.StatusBadRequest, "q is too long (max 200 chars)")
		return
	}

	results, err := a.geo.search(r.Context(), q, acceptLanguage(r))
	if err != nil {
		if errors.Is(err, errGeocodeBusy) {
			w.Header().Set("Retry-After", "2")
			writeErr(w, http.StatusServiceUnavailable, "search is busy — try again in a moment")
			return
		}
		a.log.Warn("geocode upstream error", "err", err)
		writeErr(w, http.StatusBadGateway, "geocoding service unavailable")
		return
	}

	if results == nil {
		results = []geocodeResult{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
