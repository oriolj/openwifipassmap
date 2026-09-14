package metrics

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/oriolj/openwifipassmap/internal/store"
)

// StoreCollector renders the store's business snapshot on every scrape
// (fleet-observability §5: per-scrape collector over durable state, so a
// restart never resets a number). It also reports the SQLite file sizes —
// the WAL size is what a reader of the backup note needs to know: with WAL
// mode the .db file alone is NOT the database.
type StoreCollector struct {
	store   *store.Store
	dbPath  string
	timeout time.Duration

	spots, spotsCreated, users, usersVerified, usersCreated *prometheus.Desc
	reviews, confirmations, reports, sessions, dbBytes      *prometheus.Desc
	duration, errors                                        *prometheus.Desc
	errCount                                                prometheus.Counter
}

// NewStoreCollector registers a collector over s on the package Registry.
func NewStoreCollector(s *store.Store, dbPath string) *StoreCollector {
	c := &StoreCollector{
		store: s, dbPath: dbPath, timeout: 5 * time.Second,
		spots:         prometheus.NewDesc(ns+"_spots", "Spots in the directory, by manual quality rating (0 = unrated … 3 = great).", []string{"quality"}, nil),
		spotsCreated:  prometheus.NewDesc(ns+"_spots_created", "Spots created in the trailing window.", []string{"window"}, nil),
		users:         prometheus.NewDesc(ns+"_users_total", "Registered accounts.", nil, nil),
		usersVerified: prometheus.NewDesc(ns+"_users_verified_total", "Accounts with a verified email address.", nil, nil),
		usersCreated:  prometheus.NewDesc(ns+"_users_created", "Accounts created in the trailing window.", []string{"window"}, nil),
		reviews:       prometheus.NewDesc(ns+"_reviews_total", "Reviews (rating and/or speed measurement) stored, one per spot+user.", nil, nil),
		confirmations: prometheus.NewDesc(ns+"_confirmations_total", "\"Still works\" confirmations stored, one per spot+user.", nil, nil),
		reports:       prometheus.NewDesc(ns+"_reports_total", "Open moderation reports (deleted when handled).", nil, nil),
		sessions:      prometheus.NewDesc(ns+"_sessions_active_total", "Unexpired login sessions.", nil, nil),
		dbBytes:       prometheus.NewDesc(ns+"_db_file_bytes", "Size of the SQLite files on disk (db | wal | shm).", []string{"file"}, nil),
		duration:      prometheus.NewDesc(ns+"_collector_duration_seconds", "Time the business collector took on this scrape.", nil, nil),
		errors:        prometheus.NewDesc(ns+"_collector_errors_total", "Scrapes on which the business collector failed (the other series are then absent).", nil, nil),
	}
	Registry.MustRegister(c)
	return c
}

// Describe implements prometheus.Collector.
func (c *StoreCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.spots, c.spotsCreated, c.users, c.usersVerified, c.usersCreated,
		c.reviews, c.confirmations, c.reports, c.sessions, c.dbBytes, c.duration, c.errors} {
		ch <- d
	}
}

var collectorErrors uint64

// Collect implements prometheus.Collector.
func (c *StoreCollector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	for _, f := range []struct{ label, path string }{
		{"db", c.dbPath}, {"wal", c.dbPath + "-wal"}, {"shm", c.dbPath + "-shm"},
	} {
		var size int64
		if fi, err := os.Stat(f.path); err == nil {
			size = fi.Size()
		}
		ch <- prometheus.MustNewConstMetric(c.dbBytes, prometheus.GaugeValue, float64(size), f.label)
	}

	st, err := c.store.Stats(ctx, start)
	if err != nil {
		collectorErrors++
	} else {
		for q := 0; q <= 3; q++ { // always all four buckets, so a 0 is a real 0
			ch <- prometheus.MustNewConstMetric(c.spots, prometheus.GaugeValue, float64(st.SpotsByQuality[q]), strconv.Itoa(q))
		}
		ch <- prometheus.MustNewConstMetric(c.spotsCreated, prometheus.GaugeValue, float64(st.SpotsCreated24h), "24h")
		ch <- prometheus.MustNewConstMetric(c.spotsCreated, prometheus.GaugeValue, float64(st.SpotsCreated7d), "7d")
		ch <- prometheus.MustNewConstMetric(c.users, prometheus.GaugeValue, float64(st.Users))
		ch <- prometheus.MustNewConstMetric(c.usersVerified, prometheus.GaugeValue, float64(st.UsersVerified))
		ch <- prometheus.MustNewConstMetric(c.usersCreated, prometheus.GaugeValue, float64(st.UsersCreated24h), "24h")
		ch <- prometheus.MustNewConstMetric(c.usersCreated, prometheus.GaugeValue, float64(st.UsersCreated7d), "7d")
		ch <- prometheus.MustNewConstMetric(c.reviews, prometheus.GaugeValue, float64(st.Reviews))
		ch <- prometheus.MustNewConstMetric(c.confirmations, prometheus.GaugeValue, float64(st.Confirmations))
		ch <- prometheus.MustNewConstMetric(c.reports, prometheus.GaugeValue, float64(st.Reports))
		ch <- prometheus.MustNewConstMetric(c.sessions, prometheus.GaugeValue, float64(st.ActiveSessions))
	}
	ch <- prometheus.MustNewConstMetric(c.errors, prometheus.CounterValue, float64(collectorErrors))
	ch <- prometheus.MustNewConstMetric(c.duration, prometheus.GaugeValue, time.Since(start).Seconds())
}
