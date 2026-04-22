#!/usr/bin/env bash
# govulncheck-filter.sh
# =====================
# Run `govulncheck -json ./...` from the current working directory,
# then exit:
#   0 if every reported OSV id is in the allowlist below,
#   0 if no OSVs were reported at all,
#   1 if any OSV is reported that is NOT in the allowlist (NEW vuln
#     surfaced -- that is what we actually want to break CI on).
#
# The raw govulncheck output is always printed so humans can see it
# in the job log even when we pass.
#
# Why an allowlist: GO-2024-3218 (Kademlia DHT content censorship in
# github.com/libp2p/go-libp2p-kad-dht) was flagged in 2024 with
# `Fixed in: N/A`. libp2p has not shipped a patched release, and the
# QSDM networking layer pulls in go-libp2p-kad-dht transitively
# through the standard libp2p New() constructor. Until upstream ships
# a fix we cannot upgrade past it without wholesale replacing the DHT
# backend, which is a major architectural change out of scope for a
# CI tooling commit. Suppressing ONLY this specific id (not a blanket
# "ignore all findings") keeps the scan honest: the day govulncheck
# reports GO-202X-YYYY that we *haven't* acknowledged, CI goes red.
#
# Every entry below MUST carry a tracking comment with:
#   - the OSV id
#   - a short reason (upstream unpatched / false positive / etc.)
#   - either "expires=YYYY-MM-DD" or a linked tracking issue, so the
#     allowlist doesn't silently rot forever.
set -euo pipefail

ALLOWLIST=(
  # GO-2024-3218  Content censorship in IPFS via Kademlia DHT abuse in
  #               github.com/libp2p/go-libp2p-kad-dht@v0.39.0
  #               Fixed in: N/A (upstream has no patched release)
  #               expires=2026-10-22 (re-evaluate every ~6 months)
  "GO-2024-3218"
)

# Capture both streams; we need stdout (findings) but also want to
# propagate real tool failures (missing liboqs, build errors, etc.)
# which go to stderr.
raw_out="$(mktemp)"
raw_err="$(mktemp)"
trap 'rm -f "${raw_out}" "${raw_err}"' EXIT

set +e
go run golang.org/x/vuln/cmd/govulncheck@latest -json ./... \
    >"${raw_out}" 2>"${raw_err}"
rc=$?
set -e

# Always echo stderr (build diagnostics).
if [ -s "${raw_err}" ]; then
  echo "==== govulncheck stderr ====" >&2
  cat "${raw_err}" >&2
fi

# Human-readable re-render of the JSON findings for the job log.
echo "==== govulncheck findings ===="
if command -v jq >/dev/null 2>&1; then
  jq -r 'select(.osv) | .osv.id + "  " + (.osv.summary // "(no summary)")' \
     "${raw_out}" | sort -u || true
else
  # Fallback: grep OSV ids. Works because govulncheck emits each
  # OSV object with an "id" field on its own line in -json mode.
  grep -oE '"id":"GO-[0-9]{4}-[0-9]+"' "${raw_out}" \
    | sed -E 's/"id":"(.*)"/\1/' | sort -u || true
fi
echo "=============================="

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

# Collect unique OSV ids actually reported.
if command -v jq >/dev/null 2>&1; then
  reported="$(jq -r 'select(.osv) | .osv.id' "${raw_out}" | sort -u)"
else
  reported="$(grep -oE '"id":"GO-[0-9]{4}-[0-9]+"' "${raw_out}" \
              | sed -E 's/"id":"(.*)"/\1/' | sort -u)"
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
