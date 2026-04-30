#!/usr/bin/env bash
# check-no-collapsed-env-preferred.sh
#
# Guardrail against re-introducing the rebrand-residue bug discovered
# in the audits of:
#
#   * QSDM/deploy/install_ngc_sidecar_{oci,vps}.py and
#     apps/qsdm-nvidia-ngc/validator_phase1.py (commit b0b2f77, Python),
#   * apps/qsdm-nvidia-ngc/scripts/wire-qsdm.{sh,ps1},
#     apps/qsdm-nvidia-ngc/scripts/local-attest.ps1, and
#     apps/qsdm-nvidia-ngc/docker-compose.yml (this commit, shell + YAML).
#
# Background: validator_phase1.py defines a helper
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
# to a bare os.environ.get. The same migration also produced collapsed
# documentation comments in shell / YAML scripts of the form
#
#     # QSDM_NGC_REPORT_URL / QSDM_NGC_REPORT_URL -- see wire-qsdm.{sh,ps1}
#
# (the legacy half of the slash-pair flattened to the preferred name)
# and collapsed grep alternations like
#
#     grep -E '^QSDM_NGC_INGEST_SECRET=|^QSDM_NGC_INGEST_SECRET='
#
# (regex alternation flattened so both branches match the same token).
# All three shapes are detected here.
#
# Scope:
#   * Pass A: *.py under apps/ and QSDM/deploy/ — the original
#     _env_preferred("X", "X") shape.
#   * Pass B: *.sh, *.ps1, *.yml, *.yaml, *.md, *.txt across the whole
#     tree (with the standard build-artefact exclusions) — the
#     `QSDM_X / QSDM_X` and `QSDM_X|QSDM_X` shapes, where the same
#     UPPER_SNAKE_CASE QSDM_<name> appears on both sides of `/` or `|`.
#     The QSDM_ prefix anchor keeps the pattern narrow enough not to
#     trip on legitimate alternations of unrelated literals.
#
# An ALLOWLIST handles the genuine "same identifier appears twice for
# unrelated reasons" cases (e.g. a comment that says "X / X" because
# the two halves are the same on purpose, like a tautology). At the
# time of writing the allowlist is empty; the audits cleared every
# in-tree match.
#
# Exits 0 if no collapsed pattern is found, 1 otherwise.

set -euo pipefail

if ! command -v rg >/dev/null 2>&1; then
  echo "check-no-collapsed-env-preferred: ripgrep (rg) is required" >&2
  exit 2
fi

# ---------------------------------------------------------------------------
# Pass A: Python _env_preferred("X", "X") flatten.
# ---------------------------------------------------------------------------
PATTERN_PY='_env_preferred\("([A-Z_]+)", "\1"\)'

MATCHES_PY="$(rg --pcre2 --no-heading --line-number \
                 --glob '*.py' \
                 "$PATTERN_PY" \
                 apps QSDM/deploy 2>/dev/null || true)"

# ---------------------------------------------------------------------------
# Pass B: shell / YAML / docs collapsed `QSDM_X / QSDM_X` or `QSDM_X|QSDM_X`.
#
# The regex requires:
#   * QSDM_ prefix anchor (avoids matching unrelated UPPER_SNAKE pairs)
#   * the same captured suffix on both sides (PCRE2 backref \1)
#   * a `/` or `|` separator with optional whitespace
# We deliberately do NOT match `=` because `X=X` is a legitimate
# variable-passthrough idiom in shell. Adjacent-line duplicate
# `export QSDM_X=...` patterns are out of scope (would need an
# awk pass) — the audit relies on code-review for those.
PATTERN_DOC='\bQSDM_([A-Z_]+)\b\s*[/|]\s*\bQSDM_\1\b'

MATCHES_DOC="$(rg --pcre2 --no-heading --line-number \
                  --glob '*.sh' --glob '*.ps1' \
                  --glob '*.yml' --glob '*.yaml' \
                  --glob '*.md' --glob '*.txt' \
                  --glob '!**/node_modules/**' \
                  --glob '!**/dist/**' \
                  --glob '!**/_tmp_*' \
                  --glob '!**/*.log' \
                  --glob '!**/CHANGELOG.md' \
                  --glob '!**/REBRAND_NOTES.md' \
                  --glob '!**/check-no-collapsed-env-preferred.sh' \
                  --glob '!**/rebrand-sweep.ps1' \
                  "$PATTERN_DOC" \
                  . 2>/dev/null || true)"

# CHANGELOG.md, REBRAND_NOTES.md, this script, and the rebrand-sweep
# guard intentionally contain the collapsed shape as documentation of
# the bug pattern; they are excluded above.

MATCHES="$(printf '%s\n%s\n' "$MATCHES_PY" "$MATCHES_DOC" | tr -d '\r' | awk 'NF')"

if [ -z "$MATCHES" ]; then
  echo "check-no-collapsed-env-preferred: no collapsed (X, X) / X|X / X / X patterns found (clean)"
  exit 0
fi

echo "check-no-collapsed-env-preferred: FAIL" >&2
echo "" >&2
echo "Found one or more collapsed (preferred, legacy) pair(s) where" >&2
echo "the SAME QSDM_<name> appears on both sides of a separator that" >&2
echo "should hold a (preferred, legacy) pair. This is almost always" >&2
echo "the residue of an over-eager search-and-replace (qsdmplus ->" >&2
echo "qsdm) that flattened the legacy half of the pair." >&2
echo "" >&2
echo "Offending location(s):" >&2
echo "" >&2
echo "$MATCHES" >&2
echo "" >&2
echo "Fix:" >&2
echo "  * For Python _env_preferred(\"X\", \"X\") calls: restore the" >&2
echo "    QSDMPLUS_<...> legacy name as the second argument, OR" >&2
echo "    switch to a bare os.environ.get(...) if no fallback is" >&2
echo "    needed (validator_phase1.py:_env_preferred raises" >&2
echo "    ValueError at runtime on a collapsed pair anyway)." >&2
echo "  * For docs / comments / grep alternations 'QSDM_X / QSDM_X'" >&2
echo "    or 'QSDM_X|QSDM_X': replace the second QSDM_X with the" >&2
echo "    legacy QSDMPLUS_X name (see pkg/branding/branding.go for" >&2
echo "    the canonical pairs and migration policy)." >&2
echo "  * If the duplication is genuine (e.g. an alternation of" >&2
echo "    unrelated literals that happen to share a prefix), add" >&2
echo "    the file path to the allowlist excludes in this script." >&2
exit 1
