#!/usr/bin/env bash
# check-no-new-legacy-metrics.sh
#
# Guardrail for the Prometheus metric-name prefix migration from
# `qsdm_*` to `qsdm_*` (Major Update §6, dual-emit deprecation
# window).
#
# The dual-emit machinery in `pkg/monitoring/prometheus_prefix_migration.go`
# automatically publishes every new metric under BOTH prefixes during
# the deprecation window, so any new metric introduced in code should
# be registered with its canonical `qsdm_*` name only. Hand-writing a
# `qsdm_<subsystem>_<suffix>` string in a NEW file is therefore
# almost certainly a regression -- either the author is unaware of the
# dual-emit path, or they are reviving a legacy name that the migration
# machinery is already handling.
#
# Scope:
#   1) Match only Prometheus-style metric-name literals (suffixed with
#      `_total` / `_seconds` / `_bucket` etc.) to avoid tripping on
#      branding aliases like `qsdm_node_id` in
#      `pkg/branding/branding.go`, which are NGC proof JSON field
#      names, not metrics.
#   2) Two passes:
#        (a) Go sources under QSDM/source/ (includes *.go AND *_test.go
#            because the `*.go` glob matches both; test files are NOT
#            excluded).
#        (b) Non-Go text assets anywhere in the tree: *.md, *.yml,
#            *.yaml, *.json, *.js -- the shapes that ship to operators
#            (alert rules, Grafana dashboards, runbooks, SDK tests).
#      Binary artefacts, temp files, and node_modules/dist directories
#      are skipped.
#
# Exits 0 if every legacy metric reference is in the allowlist below,
# 1 otherwise.

set -euo pipefail

# Files that are explicitly allowed to contain `qsdm_*` metric
# names. Expand this ONLY when adding a new part of the dual-emit
# machinery, a test that asserts the legacy prefix is still being
# published, or a document that deliberately references the legacy
# prefix as a migration signpost.
ALLOWLIST=(
  # --- Go: dual-emit machinery + tests that assert it still works ---
  "QSDM/source/pkg/monitoring/prometheus_prefix_migration_test.go"
  "QSDM/source/pkg/monitoring/prometheus_exp_test.go"
  "QSDM/source/pkg/monitoring/prometheus_scrape.go"
  "QSDM/source/internal/dashboard/dashboard_metrics_scrape_test.go"
  "QSDM/source/sdk/go/qsdm_test.go"
  # --- non-Go: intentional migration artefacts ---
  # JS SDK test asserts the scraper passes legacy-prefixed series
  # through unchanged (it is a transport-layer test, not a metric
  # producer).
  "QSDM/source/sdk/javascript/qsdm.test.js"
  # Example Prometheus alert rules. The file NAME itself encodes the
  # legacy prefix and the rules inside deliberately target the legacy
  # series so operators who pasted this file into their Prometheus
  # before the migration do not need to re-paste -- both prefixes
  # resolve during the dual-emit window.
  "QSDM/deploy/prometheus/alerts_qsdm.example.yml"
  # Prometheus deploy README documents that "series names use the
  # qsdm_ prefix". Keeping this in place until the legacy prefix
  # is actually retired; updating it before then would mislead
  # operators running mixed-version clusters.
  "QSDM/deploy/prometheus/README.md"
  # Starter Grafana dashboard. Same rationale as the alerts YAML: the
  # filename is `qsdm-overview.json`, it was imported by operators
  # before the rename, and every panel query targets the legacy
  # series. Re-importing mid-migration would reset panel IDs / break
  # saved links, so the dashboard stays on the legacy prefix until
  # the dual-emit window closes, at which point a v2 dashboard with
  # `qsdm_*` queries will ship as a separate file.
  "QSDM/deploy/grafana/qsdm-overview.json"
  # NGC sidecar operator QUICKSTART deliberately mentions the legacy
  # series names alongside the canonical ones as migration signposts
  # ("`qsdm_ngc_proof_ingest_accepted_total`, legacy alias
  # `qsdm_ngc_proof_ingest_accepted_total`"). Operators running
  # mixed-version clusters need both names in the doc; once the
  # legacy prefix is retired this file will drop the parenthetical
  # refs and the allowlist entry can go with it.
  "apps/qsdm-nvidia-ngc/QUICKSTART.md"
  # CHANGELOG is an append-only historical record; references to the
  # legacy prefix there are documenting the migration, not configuring
  # anything.
  "CHANGELOG.md"
)

# Regex: no leading `"` anchor so this matches both Go string literals
# ("qsdm_foo_total") and bare metric names in Prometheus alert
# rules / Grafana dashboards / operator docs (qsdm_foo_total).
# The `\b` end anchor + closed suffix list keeps it narrow enough not
# to flag non-metric identifiers like `qsdm_node_id` in the NGC
# proof wire format (no metric suffix).
REGEX='qsdm_[a-z_]+_(total|count|seconds|sum|bucket|bytes|info|ratio|current|last|active|inflight)\b'

# Two search commands, one per scope. Prefer ripgrep when available;
# fall back to git grep.
if command -v rg >/dev/null 2>&1; then
  # ripgrep handles both globs and exclusions natively.
  SEARCH_GO=(rg --no-heading --no-line-number -l
             --glob '*.go'
             "$REGEX"
             QSDM/source)
  SEARCH_NONGO=(rg --no-heading --no-line-number -l
                --glob '*.md' --glob '*.yml' --glob '*.yaml'
                --glob '*.json' --glob '*.js'
                --glob '!**/node_modules/**'
                --glob '!**/dist/**'
                --glob '!**/_tmp_*'
                --glob '!**/*.log'
                "$REGEX"
                .)
elif command -v git >/dev/null 2>&1; then
  SEARCH_GO=(git grep -l -E "$REGEX" -- 'QSDM/source/**/*.go')
  # git pathspec magic: `:!` negates, `**` matches any depth. The
  # positive pathspecs (*.md, *.yml, ...) match filenames anywhere in
  # the tree via git's default recursion semantics.
  SEARCH_NONGO=(git grep -l -E "$REGEX" --
                '*.md' '*.yml' '*.yaml' '*.json' '*.js'
                ':!**/node_modules/**' ':!**/dist/**'
                ':!**/_tmp_*' ':!**/*.log')
else
  echo "check-no-new-legacy-metrics: need either rg or git on PATH" >&2
  exit 2
fi

# ||true because rg/git grep exit 1 when there are no matches; we
# handle the "no matches" case below rather than letting set -e trip.
MATCHES_GO="$(  "${SEARCH_GO[@]}"    2>/dev/null || true)"
MATCHES_NONGO="$("${SEARCH_NONGO[@]}" 2>/dev/null || true)"
# Concat, strip CR, dedupe via awk (NOT `sort -u`).
#
# On Linux CI `sort -u` does the right thing, but when a developer runs
# this script from a Windows shell where PowerShell's PATH wins, `sort`
# can resolve to Windows' native `sort.exe` (at C:\Windows\System32),
# which does NOT understand `-u` and aborts the pipeline with
# "The system cannot find the file specified." The upstream match list
# is already computed, so the practical effect is a false-clean report.
# awk via git-bash's /usr/bin/awk is always GNU awk and sidesteps the
# name collision entirely; it also preserves first-seen order, which is
# nicer for human-readable failure output.
MATCHES="$(printf '%s\n%s\n' "$MATCHES_GO" "$MATCHES_NONGO" | tr -d '\r' | awk 'NF && !seen[$0]++' || true)"

if [ -z "$MATCHES" ]; then
  echo "check-no-new-legacy-metrics: no legacy qsdm_* metric names found (clean)"
  exit 0
fi

UNEXPECTED=""
while IFS= read -r f; do
  [ -z "$f" ] && continue
  # Normalize: strip leading "./" and flip backslashes to forward
  # slashes (Windows runners / git grep on mingw both spit mixed paths).
  norm="${f#./}"
  norm="${norm//\\//}"
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
  echo "The following file(s) contain legacy qsdm_* Prometheus metric" >&2
  echo "names but are not on the allowlist in QSDM/scripts/check-no-new-legacy-metrics.sh:" >&2
  echo "" >&2
  printf '  %s\n' $UNEXPECTED >&2
  echo "" >&2
  echo "During the dual-emit deprecation window (Major Update, section 6)," >&2
  echo "new metrics must be registered with the canonical qsdm_* prefix." >&2
  echo "The dual-emit machinery in pkg/monitoring/prometheus_prefix_migration.go" >&2
  echo "will automatically publish them under the legacy qsdm_* prefix" >&2
  echo "too for the duration of the window -- no hand-written legacy names" >&2
  echo "are needed in operator runbooks, dashboards, or alert rules." >&2
  echo "" >&2
  echo "If this literal is genuinely part of the dual-emit machinery" >&2
  echo "(e.g. a new test asserting legacy names are still published, an" >&2
  echo "example alert-rules file whose operators haven't migrated yet)," >&2
  echo "add the file to ALLOWLIST in the script." >&2
  exit 1
fi

echo "check-no-new-legacy-metrics: all legacy qsdm_* metric references are in the allowlist"
exit 0
