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

	"github.com/oriolj/openwifipassmap/internal/api"
	"github.com/oriolj/openwifipassmap/internal/email"
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
	cancel()

	mailer := email.New(env("RESEND_API_KEY", ""), env("RESEND_FROM", ""), log)

	mux := http.NewServeMux()
	a := api.New(st, *dev, log, mailer, baseURL)
	a.Routes(mux)

	webUI, err := web.New(st)
	if err != nil {
		log.Error("cannot init web UI", "err", err)
		os.Exit(1)
	}
	webUI.Routes(mux)

	handler := a.Middleware(logRequests(log, mux))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", *addr, "db", *dbPath, "dev", *dev)
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

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("req", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start).String())
	})
}
