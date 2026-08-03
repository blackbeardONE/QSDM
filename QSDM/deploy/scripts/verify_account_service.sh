#!/usr/bin/env bash
set -euo pipefail

local_url="${QSDM_ACCOUNT_LOCAL_URL:-http://127.0.0.1:8092}"
origin="${QSDM_ACCOUNT_PUBLIC_ORIGIN:-https://qsdm.tech}"
local_only=false

usage() {
  cat <<'USAGE'
Usage: verify_account_service.sh [--local-only] [--origin HTTPS_ORIGIN]

Checks the running qsdm-account service without reading or printing secrets.
The public checks also confirm that Caddy serves the account page and proxies
the account API. Set QSDM_ACCOUNT_LOCAL_URL to override the local health URL.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --local-only)
      local_only=true
      shift
      ;;
    --origin)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      origin="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

for command_name in curl python3; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "Required command is missing: $command_name" >&2
    exit 1
  }
done

check_health() {
  local base="$1"
  local payload
  payload=$(curl --fail --silent --show-error --connect-timeout 5 --max-time 12 \
    "$base/api/account/health")
  printf '%s' "$payload" | python3 -c '
import json, sys
value = json.load(sys.stdin)
if value.get("ok") is not True or value.get("service") != "qsdm-account":
    raise SystemExit("account health response has the wrong shape")
'
}

check_provider_config() {
  local base="$1"
  local payload
  payload=$(curl --fail --silent --show-error --connect-timeout 5 --max-time 12 \
    "$base/api/account/config")
  printf '%s' "$payload" | python3 -c '
import json, sys
value = json.load(sys.stdin)
login = value.get("login") or {}
if value.get("ok") is not True:
    raise SystemExit("account config response is not healthy")
if value.get("custody") != "local_wallet_only":
    raise SystemExit("account service reported an unexpected custody mode")
if not (login.get("email") is True or login.get("telegram") is True):
    raise SystemExit("account service has no enabled login provider")
'
}

check_health "$local_url"
check_provider_config "$local_url"
echo "Local QSDM Account service is healthy and has a login provider."

if [[ "$local_only" == true ]]; then
  exit 0
fi

origin="${origin%/}"
[[ "$origin" == https://* ]] || {
  echo "Public origin must use HTTPS: $origin" >&2
  exit 1
}

page=$(curl --fail --silent --show-error --connect-timeout 5 --max-time 15 \
  "$origin/account/")
grep -Fq '<title>QSDM Account</title>' <<<"$page" || {
  echo "Public account page did not return the QSDM Account document." >&2
  exit 1
}
check_health "$origin"
check_provider_config "$origin"
echo "Public QSDM Account page and API are healthy at $origin/account/."
