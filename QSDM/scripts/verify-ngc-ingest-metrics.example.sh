#!/usr/bin/env bash
# Example: curl dashboard /api/metrics/prometheus and grep qsdm_ngc_proof_ingest_* lines.
# Usage:
#   export DASHBOARD_URL=http://127.0.0.1:8081
#   export METRICS_SECRET=...   # optional Bearer for scrape auth
#   bash scripts/verify-ngc-ingest-metrics.example.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE="${DASHBOARD_URL:-http://127.0.0.1:8081}"
BASE="${BASE%/}"
URI="${BASE}/api/metrics/prometheus"
if [[ -n "${METRICS_SECRET:-}" ]]; then
  OUT="$(curl -fsS -H "Authorization: Bearer ${METRICS_SECRET}" "$URI")"
else
  OUT="$(curl -fsS "$URI")"
fi
if ! echo "$OUT" | grep -q '^qsdm_ngc_proof_ingest_'; then
  echo "No qsdm_ngc_proof_ingest_* lines (JWT or METRICS_SECRET may be required)." >&2
  exit 1
fi
echo "$OUT" | grep '^qsdm_ngc_proof_ingest_'
echo "OK: NGC proof ingest metrics visible in exposition."
