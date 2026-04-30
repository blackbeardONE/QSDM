#!/usr/bin/env bash
# check-no-collapsed-env-preferred.sh
#
# Guardrail against re-introducing the rebrand-residue bug discovered
# in the audit of QSDM/deploy/install_ngc_sidecar_{oci,vps}.py and
# apps/qsdm-nvidia-ngc/validator_phase1.py.
#
# Background: those scripts use a helper
#
#     def _env_preferred(primary: str, legacy: str) -> str:
#         return os.environ.get(primary, "").strip() \
#             or os.environ.get(legacy, "").strip()
#
# whose entire reason to exist is the (preferred, legacy) deprecation
# pair pattern. A search-and-replace migration ("qsdmplus -> qsdm")
# previously collapsed both arguments at every call site to the same
# string, e.g.
#
#     _env_preferred("QSDM_NGC_INGEST_SECRET", "QSDM_NGC_INGEST_SECRET")
#
# making the legacy fallback dead code AND making every call equivalent
# to a bare os.environ.get. The helper now raises ValueError at runtime
# when both args are equal, but that's a late defence; this script is
# the early one that fires on every PR before the change can land.
#
# Scope: every *.py file under apps/ and QSDM/deploy/. Other directories
# don't currently use the helper, but a new caller anywhere in those
# trees will be picked up automatically.
#
# Exits 0 if no collapsed call is found, 1 otherwise.

set -euo pipefail

# Match `_env_preferred("FOO", "FOO")` where the same identifier-style
# token appears on both sides. We restrict to UPPER_SNAKE_CASE because
# the helper is only ever called with env-var names; if it's ever
# extended to other shapes the regex can be loosened.
#
# The pattern uses ripgrep's PCRE2 mode for the back-reference. ripgrep
# is required by QSDM/scripts/check-no-new-legacy-metrics.sh anyway so
# the dependency is consistent with our existing CI guards.
PATTERN='_env_preferred\("([A-Z_]+)", "\1"\)'

if ! command -v rg >/dev/null 2>&1; then
  echo "check-no-collapsed-env-preferred: ripgrep (rg) is required" >&2
  exit 2
fi

MATCHES="$(rg --pcre2 --no-heading --line-number \
              --glob '*.py' \
              "$PATTERN" \
              apps QSDM/deploy 2>/dev/null || true)"

if [ -z "$MATCHES" ]; then
  echo "check-no-collapsed-env-preferred: no collapsed _env_preferred(X, X) calls found (clean)"
  exit 0
fi

echo "check-no-collapsed-env-preferred: FAIL" >&2
echo "" >&2
echo "Found _env_preferred(X, X) call(s) where the preferred and legacy" >&2
echo "argument are the SAME string. The helper exists ONLY for the" >&2
echo "(preferred, legacy) deprecation-window pattern; passing the same" >&2
echo "name twice silently kills the legacy fallback and is almost" >&2
echo "always the residue of an over-eager search-and-replace." >&2
echo "" >&2
echo "Offending location(s):" >&2
echo "" >&2
echo "$MATCHES" >&2
echo "" >&2
echo "Fix: either restore the legacy name (typically QSDMPLUS_<...>" >&2
echo "matching the QSDM_<...> preferred name -- see" >&2
echo "QSDM/source/pkg/branding/branding.go for the canonical pairs)," >&2
echo "or replace the call with a plain os.environ.get(...) if no" >&2
echo "legacy fallback is needed. The helper itself raises ValueError" >&2
echo "at runtime on a collapsed pair, so a missed fix here will crash" >&2
echo "the sidecar on first invocation." >&2
exit 1
