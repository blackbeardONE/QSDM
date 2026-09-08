#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
INSTALLER="${SCRIPT_DIR}/_install_docs_site.sh"

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

[[ -f "$INSTALLER" ]] || fail "missing installer: $INSTALLER"
bash -n "$INSTALLER"

grep -Fq 'PUBLIC_API_BASE="${QSDM_PUBLIC_API_BASE_URL:-https://api.qsdm.tech}"' "$INSTALLER" ||
  fail 'installer must accept QSDM_PUBLIC_API_BASE_URL with the production default'
grep -Fq 'PUBLIC_API_BASE="${PUBLIC_API_BASE%/}"' "$INSTALLER" ||
  fail 'installer must normalize a trailing slash from the public API base'
grep -Fq 'PUBLIC_API_BASE="${PUBLIC_API_BASE%/api/v1}"' "$INSTALLER" ||
  fail 'installer must accept either a public API root or an /api/v1 base'
grep -Fq 'CORE_STATUS_URL="${PUBLIC_API_BASE}/api/v1/status"' "$INSTALLER" ||
  fail 'installer must derive its status URL from the configured public API base'
grep -Fq '"$CORE_STATUS_URL"' "$INSTALLER" ||
  fail 'installer must use the derived public status URL for release alignment'
if grep -Fq 'https://api.qsdm.tech/api/v1/status' "$INSTALLER"; then
  fail 'installer must not hard-code the public Core status URL'
fi

printf 'docs site public API override test passed\n'