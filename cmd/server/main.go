// Command server runs the OpenWifiPassMap backend: the JSON API plus a small
// server-rendered public web UI for sharing spots.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"

	"github.com/oriolj/openwifipassmap/internal/api"
	"github.com/oriolj/openwifipassmap/internal/buildinfo"
	"github.com/oriolj/openwifipassmap/internal/email"
	"github.com/oriolj/openwifipassmap/internal/litestream"
	"github.com/oriolj/openwifipassmap/internal/metrics"
	"github.com/oriolj/openwifipassmap/internal/store"
	"github.com/oriolj/openwifipassmap/internal/web"
	"github.com/oriolj/openwifipassmap/migrations"
)

func main() {
	addr := flag.String("addr", env("ADDR", ":8080"), "listen address (0.0.0.0 for containers)")
	dbPath := flag.String("db", env("DB_PATH", "data/wifispot.db"), "SQLite database path")
	dev := flag.Bool("dev", env("DEV", "") != "", "enable permissive CORS for local frontend dev")
	flag.Parse()

	// Public origin used to build links in emails. When unset, the API derives
	// it from each request (honoring the proxy's X-Forwarded-* headers), so
	// links use the real host. Set PUBLIC_BASE_URL in prod to pin it explicitly
	// (e.g. https://openwifipassmap.oriolj.com) and be immune to Host spoofing.
	baseURL := env("PUBLIC_BASE_URL", "")
	// Email backfill address for accounts that predate the email column.
	backfillEmail := env("BACKFILL_EMAIL", "oriolj@gmail.com")

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	version := buildinfo.Get()

	// Error tracking (GlitchTip, Sentry-compatible): dormant without a DSN.
	// environment must equal the oj.env label ("prod"), release the git SHA;
	// performance tracing stays off (traces go OTel → Tempo, not here).
	if dsn := env("SENTRY_DSN", ""); dsn != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:              dsn,
			Release:          version,
			Environment:      env("SENTRY_ENVIRONMENT", "prod"),
			TracesSampleRate: 0,
		}); err != nil {
			log.Error("sentry init failed", "err", err)
		} else {
			log.Info("error tracking enabled", "release", version)
			defer sentry.Flush(2 * time.Second)
		}
	}

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Error("cannot create data dir", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Error("cannot open database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := st.Migrate(ctx, migrations.Schema); err != nil {
		cancel()
		log.Error("migration failed", "err", err)
		os.Exit(1)
	}
	if err := st.EnsureUserEmail(ctx, backfillEmail); err != nil {
		cancel()
		log.Error("email migration failed", "err", err)
		os.Exit(1)
	}
	if err := st.EnsureSpotQuality(ctx); err != nil {
		cancel()
		log.Error("quality migration failed", "err", err)
		os.Exit(1)
	}
	if err := st.EnsureReviews(ctx); err != nil {
		cancel()
		log.Error("reviews backfill failed", "err", err)
		os.Exit(1)
	}
	if err := st.EnsureAdmin(ctx); err != nil {
		cancel()
		log.Error("admin bootstrap failed", "err", err)
		os.Exit(1)
	}
	cancel()

	mailer := email.New(env("RESEND_API_KEY", ""), env("RESEND_FROM", ""), log)

	// Litestream watchdog: replication pass-through on /metrics + the
	// healthchecks.io dead-man ping (internal/litestream).
	ls := litestream.FromEnv(log)
	if ls.Enabled() {
		log.Info("running under litestream", "metrics_addr", env("LITESTREAM_METRICS_ADDR", ""),
			"s3", env("LITESTREAM_ACCESS_KEY_ID", "") != "", "heartbeat", env("HEALTHCHECKS_PING_URL_LITESTREAM", "") != "")
	} else {
		log.Warn("not running under litestream: the database is not replicated")
	}

	mux := http.NewServeMux()
	a := api.New(st, *dev, log, mailer, baseURL)
	if geocodeURL := env("GEOCODE_URL", ""); geocodeURL != "" {
		a.SetGeocodeUpstream(geocodeURL)
	}
	a.SetHealthExtra(func() map[string]string {
		if !ls.Enabled() {
			return map[string]string{"litestream": "off"}
		}
		if _, err := ls.Metrics(context.Background()); err != nil {
			return map[string]string{"litestream": "error: " + err.Error()}
		}
		return map[string]string{"litestream": "ok"}
	})
	a.Routes(mux)

	// /metrics: bearer-gated on the public origin (fails closed without
	// METRICS_TOKEN outside dev); Litestream's own families appended.
	metricsToken := metrics.TokenFromEnv()
	if metricsToken == "" && !*dev {
		log.Warn("METRICS_TOKEN unset: /metrics answers 401 to everyone until it is configured")
	}
	metrics.NewStoreCollector(st, *dbPath)
	mux.Handle("GET /metrics", metrics.Handler(metricsToken, *dev, ls.Metrics))

	// Compiled CSS + vendored JS (built by `make css`); see web/ and the
	// Dockerfile assets stage. Defaults to the in-repo path for local dev.
	staticDir := env("STATIC_DIR", "internal/web/static")
	webUI, err := web.New(st, staticDir, baseURL)
	if err != nil {
		log.Error("cannot init web UI", "err", err)
		os.Exit(1)
	}
	webUI.Routes(mux)

	// Order, outermost first: sentry (panic capture) → metrics (route label
	// read from r.Pattern after the mux matched) → CORS → access log → mux.
	var handler http.Handler = a.Middleware(logRequests(log, version, mux))
	handler = metrics.Instrument(handler)
	handler = sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle(handler) // Repanic: net/http still logs the panic

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	wdCtx, wdCancel := context.WithCancel(context.Background())
	defer wdCancel()
	go ls.Run(wdCtx, 5*time.Minute)

	go func() {
		log.Info("listening", "addr", *addr, "db", *dbPath, "dev", *dev, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Info("shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// logRequests is the access log (one line per request, Loki-friendly
// key=value) and stamps the release on every response (X-App-Version).
func logRequests(log *slog.Logger, version string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("X-App-Version", version)
		next.ServeHTTP(w, r)
		log.Info("req", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start).String())
	})
}
