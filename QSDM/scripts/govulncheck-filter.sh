#!/usr/bin/env bash
# govulncheck-filter.sh
# =====================
# Run govulncheck v1.6.0 in JSON mode from the current working directory,
# then exit:
#   0 if every reported OSV id is in the allowlist below,
#   0 if no OSVs were reported at all,
#   1 if any OSV is reported that is NOT in the allowlist (NEW vuln
#     surfaced -- that is what we actually want to break CI on).
#
# Scanner diagnostics and affected-finding summaries are always printed so
# humans can see the classification in the job log even when the gate passes.
#
# Allowlist posture: intentionally empty. GO-2024-3218 used to be tracked
# here while QSDM used go-libp2p-kad-dht for WAN discovery. That DHT path was
# removed in favor of explicit bootstrap-peer dialing, so any reachable
# govulncheck finding should now fail this gate.
#
# Every entry below MUST carry a tracking comment with:
#   - the OSV id
#   - a short reason (upstream unpatched / false positive / etc.)
#   - either "expires=YYYY-MM-DD" or a linked tracking issue, so the
#     allowlist doesn't silently rot forever.
set -euo pipefail

ALLOWLIST=()

# Capture both streams; we need stdout (findings) but also want to
# propagate real tool failures (missing liboqs, build errors, etc.)
# which go to stderr.
raw_out="$(mktemp)"
raw_err="$(mktemp)"
trap 'rm -f "${raw_out}" "${raw_err}"' EXIT

set +e
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 -json ./... \
    >"${raw_out}" 2>"${raw_err}"
rc=$?
set -e

# Always echo stderr (build diagnostics).
if [ -s "${raw_err}" ]; then
  echo "==== govulncheck stderr ====" >&2
  cat "${raw_err}" >&2
fi

# Important: govulncheck's -json mode emits TWO kinds of records that
# carry an OSV id:
#
#   1. {"osv": {"id": "GO-...", ...}}     -- a raw advisory database
#      entry. govulncheck streams the entire catalog that matched by
#      MODULE VERSION. Most of these are NOT reachable from our call
#      graph. Filtering on this field alone yields 100+ false positives.
#
#   2. {"finding": {"osv": "GO-...", ...}} -- a module, package, or
#      symbol finding. A module-only notice has a one-frame trace containing
#      module/version but no package. Package and symbol findings include a
#      package path and demonstrate that affected code enters QSDM's build.
#
# Only package/symbol records from (2) should gate CI. The earlier versions
# keyed off either (1) or any non-empty trace; the latter incorrectly counted
# module-only notices emitted by newer scanners.
findings_jq='select(any(.finding.trace[]?; (.package // "") != "")) | .finding.osv'

# Human-readable re-render of affected package/symbol findings for the log.
echo "==== govulncheck affected package/symbol findings ===="
if command -v jq >/dev/null 2>&1; then
  # Pair each finding id with the first OSV summary we saw for it, so
  # the log is self-explanatory without having to cross-reference.
  jq -r --slurp '
    (map(select(.osv))     | map({(.osv.id): (.osv.summary // "(no summary)")}) | add) as $summaries
    | (map(select(any(.finding.trace[]?; (.package // "") != ""))) | map(.finding.osv) | unique) as $hits
    | $hits[] | . + "  " + ($summaries[.] // "(no summary)")
  ' "${raw_out}" || true
else
  echo "jq is required to distinguish reachable traces from module notices" >&2
  exit 2
fi
echo "========================================"

# If govulncheck itself crashed (exit != 0 AND != 3), propagate.
# govulncheck exit codes:
#   0 -> no vulns
#   2 -> unexpected processing error (want to fail CI loudly)
#   3 -> vulns found
# We treat 0 as clean, 3 as "maybe allowlisted", anything else as fatal.
if [ "${rc}" -ne 0 ] && [ "${rc}" -ne 3 ]; then
  echo "govulncheck failed with exit code ${rc} (not a vuln report)" >&2
  exit "${rc}"
fi

# Collect unique OSV ids that reach an imported package (not the raw catalog).
if command -v jq >/dev/null 2>&1; then
  reported="$(jq -r "${findings_jq}" "${raw_out}" | sort -u)"
else
  echo "jq is required to distinguish reachable traces from module notices" >&2
  exit 2
fi

if [ -z "${reported}" ]; then
  echo "govulncheck: no vulnerabilities reported. CLEAN."
  exit 0
fi

# Anything reported AND not in allowlist is fatal.
unexpected=()
while IFS= read -r id; do
  [ -z "${id}" ] && continue
  ok=0
  for allow in "${ALLOWLIST[@]}"; do
    if [ "${id}" = "${allow}" ]; then
      ok=1
      break
    fi
  done
  if [ "${ok}" -eq 0 ]; then
    unexpected+=("${id}")
  fi
done <<< "${reported}"

if [ "${#unexpected[@]}" -gt 0 ]; then
  echo "govulncheck: UNEXPECTED vulnerabilities (not in allowlist):" >&2
  for id in "${unexpected[@]}"; do
    echo "  - ${id}" >&2
  done
  echo "" >&2
  echo "Either upgrade past them or add the id to the allowlist in" >&2
  echo "QSDM/scripts/govulncheck-filter.sh with a tracking comment." >&2
  exit 1
fi

echo "govulncheck: all reported vulnerabilities are allowlisted; accepting."
exit 0
