#!/usr/bin/env bash
# check-no-new-legacy-metrics.sh
#
# Guardrail for the Prometheus metric-name prefix migration from
# `qsdmplus_*` to `qsdm_*` (Major Update §6, dual-emit deprecation
# window).
#
# The dual-emit machinery in `pkg/monitoring/prometheus_prefix_migration.go`
# automatically publishes every new metric under BOTH prefixes during
# the deprecation window, so any new metric introduced in code should
# be registered with its canonical `qsdm_*` name only. Hand-writing a
# `qsdmplus_<subsystem>_<suffix>` string in a NEW file is therefore
# almost certainly a regression -- either the author is unaware of the
# dual-emit path, or they are reviving a legacy name that the migration
# machinery is already handling.
#
# This script is deliberately narrow: it matches only Prometheus-style
# metric-name literals (suffixed with `_total` / `_seconds` / `_bucket`
# etc.) to avoid tripping on branding aliases like
# `qsdmplus_node_id` in `pkg/branding/branding.go`, which are NGC proof
# JSON field names, not metrics.
#
# Exits 0 if every legacy metric reference is in the allowlist below,
# 1 otherwise.

set -euo pipefail

# Files that are explicitly allowed to contain `qsdmplus_*` metric
# names. Expand this ONLY when adding a new part of the dual-emit
# machinery or a test that asserts the legacy prefix is still being
# published. Everything else should use the canonical `qsdm_*` prefix.
#
# Keep this list tight: every entry should genuinely need a hand-written
# `qsdmplus_<name>_<suffix>` literal. Files that just reference the
# prefix abstractly (e.g. `const legacyPrefix = "qsdmplus_"`) build
# metric names via concatenation at runtime and never trip the regex
# below, so they should NOT be allowlisted "defensively".
ALLOWLIST=(
  "QSDM/source/pkg/monitoring/prometheus_prefix_migration_test.go"
  "QSDM/source/pkg/monitoring/prometheus_exp_test.go"
  "QSDM/source/pkg/monitoring/prometheus_scrape.go"
  "QSDM/source/internal/dashboard/dashboard_metrics_scrape_test.go"
  "QSDM/source/sdk/go/qsdmplus_test.go"
)

# Prefer ripgrep when available (faster + saner glob syntax); fall back
# to git grep so this works on minimal images and local machines that
# don't have rg installed yet.
if command -v rg >/dev/null 2>&1; then
  SEARCH_CMD=(rg --no-heading --no-line-number -l
              --glob '*.go'
              '"qsdmplus_[a-z_]+_(total|count|seconds|sum|bucket|bytes|info|ratio|current|last|active|inflight)"'
              QSDM/source)
elif command -v git >/dev/null 2>&1; then
  SEARCH_CMD=(git grep -l -E
              '"qsdmplus_[a-z_]+_(total|count|seconds|sum|bucket|bytes|info|ratio|current|last|active|inflight)"'
              -- 'QSDM/source/**/*.go')
else
  echo "check-no-new-legacy-metrics: need either rg or git on PATH" >&2
  exit 2
fi

# ||true because rg/git grep exit 1 when there are no matches; we
# handle the "no matches" case below rather than letting set -e trip.
MATCHES="$("${SEARCH_CMD[@]}" 2>/dev/null | tr -d '\r' | sort -u || true)"

if [ -z "$MATCHES" ]; then
  echo "check-no-new-legacy-metrics: no legacy qsdmplus_* metric names found (clean)"
  exit 0
fi

UNEXPECTED=""
while IFS= read -r f; do
  [ -z "$f" ] && continue
  # Normalize to forward slashes for comparison on Windows runners.
  norm="${f//\\//}"
  allowed="no"
  for a in "${ALLOWLIST[@]}"; do
    if [ "$norm" = "$a" ]; then
      allowed="yes"
      break
    fi
  done
  if [ "$allowed" = "no" ]; then
    UNEXPECTED="${UNEXPECTED}${norm}"$'\n'
  fi
done <<< "$MATCHES"

if [ -n "$UNEXPECTED" ]; then
  echo "check-no-new-legacy-metrics: FAIL" >&2
  echo "" >&2
  echo "The following file(s) contain legacy qsdmplus_* Prometheus metric" >&2
  echo "names but are not on the allowlist in QSDM/scripts/check-no-new-legacy-metrics.sh:" >&2
  echo "" >&2
  printf '  %s\n' $UNEXPECTED >&2
  echo "" >&2
  echo "During the dual-emit deprecation window (Major Update, section 6)," >&2
  echo "new metrics must be registered with the canonical qsdm_* prefix." >&2
  echo "The dual-emit machinery in pkg/monitoring/prometheus_prefix_migration.go" >&2
  echo "will automatically publish them under the legacy qsdmplus_* prefix" >&2
  echo "too for the duration of the window -- no hand-written legacy names" >&2
  echo "are needed." >&2
  echo "" >&2
  echo "If this literal is genuinely part of the dual-emit machinery" >&2
  echo "(e.g. a new test asserting legacy names are still published)," >&2
  echo "add the file to ALLOWLIST in the script." >&2
  exit 1
fi

echo "check-no-new-legacy-metrics: all legacy qsdmplus_* metric references are in the allowlist"
exit 0
