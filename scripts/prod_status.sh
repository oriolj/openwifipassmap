#!/usr/bin/env bash
# One-screen production status from the estate stack (Prometheus on monitor-1-nc, tailnet).
# Usage: scripts/prod_status.sh [project]   (make prod-status)
# Every line reads the hub; until the openwifipassmap-app scrape job is live
# (hq-monitoring, needs METRICS_TOKEN on both sides) the Prometheus lines print "-".
set -euo pipefail
P="${1:-openwifipassmap}"
PROM="${PROM:-http://monitor-1-nc:9090}"
HEALTH="${HEALTH_URL:-https://openwifipassmap.oriolj.com/api/health}"
M="${P}_"   # metric prefix

HERE="$(cd "$(dirname "$0")" && pwd)"
q() {  # q '<promql>' [--keep-job] -> "label/label=value, ..." on one line
  curl -s -m 8 -G "$PROM/api/v1/query" --data-urlencode "query=$1" | python3 "$HERE/promq.py" ${2:-}
}

echo "health:     $(curl -s -m 8 "$HEALTH")"
echo "targets:    $(q "up{project=\"$P\"}" --keep-job)"
echo "version:    $(q "${M}app_info{project=\"$P\"}")"
echo "req/s 15m:  $(q "sum(rate(${M}http_request_duration_seconds_count{project=\"$P\"}[15m]))")"
echo "5xx ratio:  $(q "(sum(rate(${M}http_request_duration_seconds_count{project=\"$P\",code=~\"5..\"}[15m])) or vector(0)) / sum(rate(${M}http_request_duration_seconds_count{project=\"$P\"}[15m]))")"
echo "p95 15m:    $(q "histogram_quantile(0.95, sum by (le) (rate(${M}http_request_duration_seconds_bucket{project=\"$P\"}[15m])))")s"
echo "in-flight:  $(q "${M}http_requests_in_flight{project=\"$P\"}")"
echo "spots:      total=$(q "sum(${M}spots{project=\"$P\"})") by_quality=[$(q "${M}spots{project=\"$P\"}")] created=[$(q "${M}spots_created{project=\"$P\"}")]"
echo "users:      total=$(q "${M}users_total{project=\"$P\"}") verified=$(q "${M}users_verified_total{project=\"$P\"}") created=[$(q "${M}users_created{project=\"$P\"}")] sessions=$(q "${M}sessions_active_total{project=\"$P\"}")"
echo "community:  reviews=$(q "${M}reviews_total{project=\"$P\"}") confirmations=$(q "${M}confirmations_total{project=\"$P\"}") open_reports=$(q "${M}reports_total{project=\"$P\"}")"
echo "nearby/h:   $(q "sum(increase(${M}http_request_duration_seconds_count{project=\"$P\",route=\"GET /api/spots/nearby\"}[1h]))")  uploads/24h: $(q "sum(increase(${M}http_request_duration_seconds_count{project=\"$P\",route=\"POST /api/spots\",code=\"201\"}[24h]))")  geocode/h: [$(q "sum by (result) (increase(${M}geocode_requests_total{project=\"$P\"}[1h]))")]"
echo "rate-limit: 24h=[$(q "sum by (route) (increase(${M}rate_limited_total{project=\"$P\"}[24h]))")]  emails 24h=[$(q "sum by (kind,result) (increase(${M}emails_total{project=\"$P\"}[24h]))")]"
echo "sqlite:     db=$(q "${M}db_file_bytes{project=\"$P\",file=\"db\"}")B wal=$(q "${M}db_file_bytes{project=\"$P\",file=\"wal\"}")B collector_errors=$(q "${M}collector_errors_total{project=\"$P\"}")"
echo "litestream: up=$(q "${M}litestream_up{project=\"$P\"}") s3=$(q "${M}litestream_s3_configured{project=\"$P\"}") sync_errors=$(q "sum(litestream_sync_error_count{project=\"$P\"})") replica_wal_bytes=[$(q "litestream_replica_wal_bytes{project=\"$P\"}")] heartbeat=[$(q "${M}litestream_heartbeat_pings_total{project=\"$P\"}")]"
