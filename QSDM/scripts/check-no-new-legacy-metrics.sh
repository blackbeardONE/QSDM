#!/usr/bin/env bash
# check-no-new-legacy-metrics.sh
#
# Guardrail against re-introducing the retired Prometheus metric-name
# prefix `qsdmplus_*` (Major Update §6, "qsdmplus -> qsdm" rebrand,
# completed in commit db9b590).
#
# The dual-emit window closed with the rebrand: `pkg/monitoring/
# prometheus_prefix_migration.go` was deleted, every canonical metric
# now uses the `qsdm_*` prefix only, and no `qsdmplus_*` literals remain
# in the source tree. This script's job is to keep it that way -- a
# new file containing a `qsdmplus_<subsystem>_<suffix>` literal is
# almost certainly a regression, either a stale rebase from a
# pre-rebrand branch or a paste from an outdated runbook.
#
# Hand-written `qsdm_*` literals are EXPECTED and not flagged: the
# canonical prefix is the only prefix we publish today.
#
# Scope:
#   1) Match only Prometheus-style metric-name literals (suffixed with
#      `_total` / `_seconds` / `_bucket` etc.) to avoid tripping on
#      branding aliases like `qsdmplus_node_id` historically present
#      in NGC proof JSON field names (no metric suffix).
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

# Files that are explicitly allowed to contain `qsdmplus_*` metric
# names. With the rebrand complete, no in-tree files actually need
# this exemption today (every Go source / dashboard / alert rule
# uses the canonical `qsdm_*` prefix). The allowlist is preserved as
# documentation: if a future migration ever needs to re-emit the
# retired prefix from a specific test or operator runbook, this is
# where it goes.
ALLOWLIST=(
  # CHANGELOG is an append-only historical record; documenting the
  # retired prefix (e.g. "renamed qsdmplus_foo to qsdm_foo") is
  # legitimate use that should not trip the guard.
  "CHANGELOG.md"
  # Rebrand notes documents the migration verbatim, including
  # before/after metric examples.
  "QSDM/docs/docs/REBRAND_NOTES.md"
)

# Regex: no leading `"` anchor so this matches both Go string literals
# ("qsdmplus_foo_total") and bare metric names in Prometheus alert
# rules / Grafana dashboards / operator docs (qsdmplus_foo_total).
# The `\b` end anchor + closed suffix list keeps it narrow enough not
# to flag non-metric identifiers like `qsdmplus_node_id` in the
# (historical) NGC proof wire format (no metric suffix).
REGEX='qsdmplus_[a-z_]+_(total|count|seconds|sum|bucket|bytes|info|ratio|current|last|active|inflight)\b'

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
  echo "check-no-new-legacy-metrics: no legacy qsdmplus_* metric names found (clean)"
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
  echo "The following file(s) contain legacy qsdmplus_* Prometheus metric" >&2
  echo "names but are not on the allowlist in QSDM/scripts/check-no-new-legacy-metrics.sh:" >&2
  echo "" >&2
  printf '  %s\n' $UNEXPECTED >&2
  echo "" >&2
  echo "The qsdmplus_* prefix was retired in the Major Update rebrand" >&2
  echo "(commit db9b590). Every canonical Prometheus series now uses" >&2
  echo "the qsdm_* prefix; no hand-written legacy names are needed in" >&2
  echo "Go sources, operator runbooks, dashboards, or alert rules." >&2
  echo "" >&2
  echo "If this literal is genuinely required (e.g. a CHANGELOG entry" >&2
  echo "documenting the rename, an operator runbook documenting the" >&2
  echo "retired prefix), add the file to ALLOWLIST in the script." >&2
  exit 1
fi

echo "check-no-new-legacy-metrics: all legacy qsdmplus_* metric references are in the allowlist"
exit 0
