// Package metrics exposes the server's Prometheus metrics: HTTP golden signals
// (latency histogram by route/method/code, in-flight gauge), a few event
// counters wired at their call sites (rate-limit rejections, geocoding,
// emails), the release identifier, Go runtime/process series, and a
// per-scrape business collector over the store (spots, users, reviews …).
//
// Contract with the estate (fleet-observability skill): every series is
// prefixed `openwifipassmap_`, labels are bounded (routes are the mux
// patterns, never raw URLs), and /metrics is bearer-token gated on the
// public origin — it fails CLOSED when METRICS_TOKEN is unset outside dev.
// Catalogue: METRICS.md at the repo root.
package metrics

import (
	"context"
	"crypto/subtle"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/common/expfmt"

	"github.com/oriolj/openwifipassmap/internal/buildinfo"
)

const ns = "openwifipassmap"

// Registry is the process registry — a dedicated one so the exposition holds
// exactly what this file registers (no stray default-registry series).
var Registry = prometheus.NewRegistry()

var (
	// HTTPDuration is the request latency histogram. `route` is the matched
	// ServeMux pattern ("GET /api/spots/nearby"), so cardinality is the
	// route table, not the URL space; "unmatched" for 404s outside it.
	HTTPDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns, Name: "http_request_duration_seconds",
		Help:    "HTTP request latency by mux route, method and status code (health and metrics routes excluded).",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
	}, []string{"route", "method", "code"})

	// HTTPInFlight counts requests currently being served.
	HTTPInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "http_requests_in_flight",
		Help: "Requests currently being served (health and metrics routes excluded).",
	})

	// RateLimited counts requests rejected with 429 by a per-IP bucket.
	RateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "rate_limited_total",
		Help: "Requests rejected with 429 by the per-IP rate limiters, by route.",
	}, []string{"route"})

	// Geocode counts address searches by outcome: served from the in-memory
	// cache, fetched from Nominatim, refused because the outbound throttle
	// queue was full, or failed upstream.
	Geocode = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "geocode_requests_total",
		Help: "Geocoding searches by outcome (cache | upstream | busy | error).",
	}, []string{"result"})

	// Emails counts transactional mail attempts by kind and result.
	Emails = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "emails_total",
		Help: "Transactional emails by kind (verification | reset) and result (sent | error).",
	}, []string{"kind", "result"})

	// LitestreamUp is 1 when the Litestream metrics endpoint answered on the
	// last watchdog tick (replication process alive), 0 otherwise; absent
	// when the server does not run under Litestream.
	LitestreamUp = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "litestream_up",
		Help: "1 when the in-container Litestream replicator answered its metrics endpoint on the last check.",
	})

	// LitestreamS3Configured is 1 when off-host (S3/R2) replication
	// credentials are present — the difference between "replicated to the
	// same disk" and "backed up".
	LitestreamS3Configured = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: ns, Name: "litestream_s3_configured",
		Help: "1 when Litestream has an S3-compatible (off-host) replica configured.",
	})

	// LitestreamHeartbeats counts healthchecks.io pings by result.
	LitestreamHeartbeats = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "litestream_heartbeat_pings_total",
		Help: "healthchecks.io pings sent by the Litestream watchdog, by result (ok | fail | ping_error).",
	}, []string{"result"})

	appInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: ns, Name: "app_info",
		Help: "Build information; value is always 1. `version` is the deployed git SHA.",
	}, []string{"version"})
)

func init() {
	Registry.MustRegister(
		HTTPDuration, HTTPInFlight, RateLimited, Geocode, Emails,
		LitestreamUp, LitestreamS3Configured, LitestreamHeartbeats, appInfo,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	appInfo.WithLabelValues(buildinfo.Get()).Set(1)
	// Pre-create the label sets the dashboard/alerts read, so a fresh
	// process exposes 0 instead of nothing.
	for _, r := range []string{"cache", "upstream", "busy", "error"} {
		Geocode.WithLabelValues(r)
	}
	for _, r := range []string{"ok", "fail", "ping_error"} {
		LitestreamHeartbeats.WithLabelValues(r)
	}
}

// excluded returns true for routes that are never counted: the container
// health probe (every 15 s) and the scrape itself would drown real traffic.
func excluded(path string) bool {
	return path == "/api/health" || path == "/metrics"
}

// Instrument wraps the root handler with the latency histogram and the
// in-flight gauge. It must sit OUTSIDE the mux: the route label is read from
// r.Pattern, which the ServeMux fills on the same *Request before
// dispatching.
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if excluded(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		HTTPInFlight.Inc()
		defer HTTPInFlight.Dec()
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		HTTPDuration.WithLabelValues(route, r.Method, strconv.Itoa(sw.code)).
			Observe(time.Since(start).Seconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.code = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach Flush/Hijack on the real writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Extra supplies additional exposition text appended after the registry's
// families (used for the Litestream pass-through). Errors are ignored: the
// scrape must never fail because a sidecar did.
type Extra func(ctx context.Context) ([]byte, error)

// Handler serves /metrics. Access policy (fleet-observability §5, the
// public-origin token path): a request must carry `Authorization: Bearer
// <token>`; with no token configured the endpoint answers 401 to everyone
// unless dev is set (local runs), i.e. it fails closed in production.
func Handler(token string, dev bool, extra Extra) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token, dev) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		families, err := Registry.Gather()
		if err != nil {
			http.Error(w, "gather: "+err.Error(), http.StatusInternalServerError)
			return
		}
		format := expfmt.NewFormat(expfmt.TypeTextPlain)
		w.Header().Set("Content-Type", string(format))
		enc := expfmt.NewEncoder(w, format)
		for _, f := range families {
			if err := enc.Encode(f); err != nil {
				return
			}
		}
		if extra != nil {
			if b, err := extra(r.Context()); err == nil && len(b) > 0 {
				_, _ = io.WriteString(w, "\n")
				_, _ = w.Write(b)
			}
		}
	})
}

func authorized(r *http.Request, token string, dev bool) bool {
	if token == "" {
		return dev
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	got := strings.TrimPrefix(h, "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

// TokenFromEnv reads METRICS_TOKEN.
func TokenFromEnv() string { return os.Getenv("METRICS_TOKEN") }
