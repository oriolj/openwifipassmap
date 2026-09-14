// Package litestream watches the Litestream replicator that wraps the server
// in production (docker/entrypoint.sh: `litestream replicate -exec
// /app/server`). Litestream serves its own Prometheus metrics on a loopback
// port inside the container; this package (1) passes those families through
// the app's /metrics so the hub sees replication state without a second
// scrape, and (2) runs a dead-man watchdog that pings a healthchecks.io
// check while replication is healthy — a dead or erroring replicator turns
// the check red instead of staying silent.
package litestream

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/oriolj/openwifipassmap/internal/metrics"
)

// Watchdog polls the replicator's metrics endpoint.
type Watchdog struct {
	addr     string // host:port of Litestream's `addr:` listener, "" = not running under Litestream
	pingURL  string // healthchecks.io ping URL, "" = no heartbeat
	log      *slog.Logger
	client   *http.Client
	lastErrs float64
	primed   bool
}

// FromEnv builds a Watchdog from LITESTREAM_METRICS_ADDR (set by the
// entrypoint when it runs the server under Litestream) and
// HEALTHCHECKS_PING_URL_LITESTREAM. Enabled reports whether there is
// anything to watch.
func FromEnv(log *slog.Logger) *Watchdog {
	w := &Watchdog{
		addr:    os.Getenv("LITESTREAM_METRICS_ADDR"),
		pingURL: strings.TrimRight(os.Getenv("HEALTHCHECKS_PING_URL_LITESTREAM"), "/"),
		log:     log,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
	if os.Getenv("LITESTREAM_ACCESS_KEY_ID") != "" {
		metrics.LitestreamS3Configured.Set(1)
	} else {
		metrics.LitestreamS3Configured.Set(0)
	}
	return w
}

// Enabled is true when the server runs under Litestream.
func (w *Watchdog) Enabled() bool { return w.addr != "" }

// Metrics fetches the replicator's exposition, filtered to the litestream_*
// families, for pass-through on the app's /metrics. Also updates the
// openwifipassmap_litestream_up gauge.
func (w *Watchdog) Metrics(ctx context.Context) ([]byte, error) {
	if !w.Enabled() {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+w.addr+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	res, err := w.client.Do(req)
	if err != nil {
		metrics.LitestreamUp.Set(0)
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil || res.StatusCode != http.StatusOK {
		metrics.LitestreamUp.Set(0)
		return nil, err
	}
	metrics.LitestreamUp.Set(1)
	return Filter(body), nil
}

// Filter keeps only the litestream_* families (samples and their HELP/TYPE
// headers) from a text exposition, dropping Litestream's own go_/process_
// series which would collide with the app's.
func Filter(exposition []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(exposition, []byte("\n")) {
		s := string(line)
		switch {
		case strings.HasPrefix(s, "litestream_"),
			strings.HasPrefix(s, "# HELP litestream_"),
			strings.HasPrefix(s, "# TYPE litestream_"):
			out.Write(line)
			out.WriteByte('\n')
		}
	}
	return out.Bytes()
}

// SyncErrors sums litestream_sync_error_count over a filtered exposition.
func SyncErrors(exposition []byte) float64 {
	var total float64
	for _, line := range strings.Split(string(exposition), "\n") {
		if !strings.HasPrefix(line, "litestream_sync_error_count") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[len(fields)-1], 64); err == nil {
			total += v
		}
	}
	return total
}

// Run evaluates replication every interval and pings healthchecks.io: the
// success URL when the replicator answers and no new sync error appeared
// since the previous tick, `/fail` otherwise. Blocks until ctx is done.
// The ping sits on the success path only (healthchecks-io skill): an
// exception or a silent process death can never report healthy.
func (w *Watchdog) Run(ctx context.Context, interval time.Duration) {
	if !w.Enabled() {
		return
	}
	// Litestream starts its metrics listener and the child process together;
	// give it a moment so the first tick is not a false "unreachable".
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
	}
	w.tick(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *Watchdog) tick(ctx context.Context) {
	body, err := w.Metrics(ctx)
	if err != nil {
		w.log.Error("litestream watchdog: replicator unreachable", "err", err)
		w.ping(ctx, false)
		return
	}
	errs := SyncErrors(body)
	healthy := !w.primed || errs <= w.lastErrs
	if !healthy {
		w.log.Error("litestream watchdog: sync errors increased", "errors", errs, "previous", w.lastErrs)
	}
	w.lastErrs, w.primed = errs, true
	w.ping(ctx, healthy)
}

func (w *Watchdog) ping(ctx context.Context, ok bool) {
	if w.pingURL == "" {
		return
	}
	url := w.pingURL
	result := "ok"
	if !ok {
		url += "/fail"
		result = "fail"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}
	res, err := w.client.Do(req)
	if err != nil {
		metrics.LitestreamHeartbeats.WithLabelValues("ping_error").Inc()
		w.log.Warn("litestream watchdog: healthchecks ping failed", "err", err)
		return
	}
	res.Body.Close()
	metrics.LitestreamHeartbeats.WithLabelValues(result).Inc()
}
