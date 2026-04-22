#!/usr/bin/env bash
# Example: curl dashboard Prometheus text and show qsdmplus_submesh_* lines.
# Prerequisites: node running, dashboard up (default port 8081).
#
# Usage:
#   chmod +x scripts/verify-submesh-metrics.example.sh
#   ./scripts/verify-submesh-metrics.example.sh
#   DASHBOARD_URL=http://127.0.0.1:9091 METRICS_SECRET='your-scrape-secret' ./scripts/verify-submesh-metrics.example.sh
set -euo pipefail
BASE="${DASHBOARD_URL:-http://127.0.0.1:8081}"
URL="${BASE%/}/api/metrics/prometheus"
if [[ -n "${METRICS_SECRET:-}" ]]; then
  OUT="$(curl -sfS -H "Authorization: Bearer ${METRICS_SECRET}" "$URL")"
else
  OUT="$(curl -sfS "$URL")"
fi
echo "$OUT" | grep -E '^qsdmplus_submesh_' || {
  echo "No qsdmplus_submesh_* lines found (need JWT or metrics_scrape_secret for this URL?)." >&2
  exit 1
}
echo "OK: submesh metrics visible in exposition."
