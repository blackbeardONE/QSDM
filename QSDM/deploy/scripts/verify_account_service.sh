#!/usr/bin/env bash
set -euo pipefail

local_url="${QSDM_ACCOUNT_LOCAL_URL:-http://127.0.0.1:8092}"
origin="${QSDM_ACCOUNT_PUBLIC_ORIGIN:-https://qsdm.tech}"
local_only=false
activation_email=""
check_telegram=false

usage() {
  cat <<'USAGE'
Usage: verify_account_service.sh [OPTIONS]

Checks the running qsdm-account service without reading or printing secrets.
The public checks also confirm that Caddy serves the account page and proxies
the account API. Set QSDM_ACCOUNT_LOCAL_URL to override the local health URL.

Options:
  --local-only                 Skip public HTTPS and Caddy checks.
  --origin HTTPS_ORIGIN        Override the public origin.
  --activation-email ADDRESS   Send a real one-time sign-in link.
  --check-telegram             Check Telegram authorization routing and JWKS.
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
    --activation-email)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      activation_email="$2"
      shift 2
      ;;
    --check-telegram)
      check_telegram=true
      shift
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
email = login.get("email") is True
telegram = login.get("telegram") is True
if not (email or telegram):
    raise SystemExit("account service has no enabled login provider")
print("1" if email else "0", "1" if telegram else "0")
'
}

check_public_security_headers() {
  local target="$1"
  local headers
  headers=$(mktemp)
  if ! curl --fail --silent --show-error --connect-timeout 5 --max-time 15 \
    --dump-header "$headers" --output /dev/null "$target"; then
    rm -f "$headers"
    return 1
  fi
  if ! python3 - "$headers" <<'PY'
import sys

headers = {}
with open(sys.argv[1], encoding="utf-8", errors="replace") as source:
    for line in source:
        if ":" not in line:
            continue
        name, value = line.split(":", 1)
        headers.setdefault(name.strip().lower(), []).append(value.strip().lower())

def contains(name, value):
    return any(value in item for item in headers.get(name, []))

requirements = [
    (contains("cache-control", "no-store"), "Cache-Control: no-store"),
    (contains("content-security-policy", "frame-ancestors 'none'"), "CSP frame-ancestors 'none'"),
    (contains("permissions-policy", "camera=()"), "Permissions-Policy camera=()"),
    (
        contains("referrer-policy", "no-referrer")
        or contains("referrer-policy", "strict-origin-when-cross-origin")
        or contains("referrer-policy", "same-origin"),
        "a restrictive Referrer-Policy",
    ),
    (contains("strict-transport-security", "max-age="), "Strict-Transport-Security"),
    (contains("x-content-type-options", "nosniff"), "X-Content-Type-Options: nosniff"),
]
missing = [label for ok, label in requirements if not ok]
if missing:
    raise SystemExit("public account response is missing: " + ", ".join(missing))
PY
  then
    rm -f "$headers"
    return 1
  fi
  rm -f "$headers"
}

trigger_activation_email() {
  local base="$1"
  local recipient="$2"
  local request response
  request=$(python3 -c 'import json,sys; print(json.dumps({"email": sys.argv[1]}))' "$recipient")
  response=$(curl --fail --silent --show-error --connect-timeout 5 --max-time 30 \
    --request POST --header 'Content-Type: application/json' \
    --data "$request" "$base/api/account/email/start")
  printf '%s' "$response" | python3 -c '
import json, sys
value = json.load(sys.stdin)
if value.get("ok") is not True:
    raise SystemExit("email activation request was not accepted")
'
}

check_telegram_provider() {
  local base="$1"
  local expected_origin="$2"
  local headers status location jwks
  headers=$(mktemp)
  status=$(curl --silent --show-error --connect-timeout 5 --max-time 15 \
    --dump-header "$headers" --output /dev/null --write-out '%{http_code}' \
    "$base/api/account/telegram/start")
  if [[ "$status" != "302" ]]; then
    rm -f "$headers"
    echo "Telegram start route returned HTTP $status instead of 302." >&2
    return 1
  fi
  if ! location=$(python3 - "$headers" <<'PY'
import sys
locations = []
with open(sys.argv[1], encoding="utf-8", errors="replace") as source:
    for line in source:
        if line.lower().startswith("location:"):
            locations.append(line.split(":", 1)[1].strip())
if not locations:
    raise SystemExit("Telegram start response did not include a Location header")
print(locations[-1])
PY
  ); then
    rm -f "$headers"
    return 1
  fi
  rm -f "$headers"
  python3 - "$location" "${expected_origin%/}/api/account/telegram/callback" <<'PY'
import sys
from urllib.parse import parse_qs, urlparse

target = urlparse(sys.argv[1])
query = parse_qs(target.query)
if target.scheme != "https" or target.netloc != "oauth.telegram.org" or target.path != "/auth":
    raise SystemExit("Telegram authorization redirect has an unexpected destination")
required = ("client_id", "state", "nonce", "code_challenge")
if any(not query.get(name, [""])[0] for name in required):
    raise SystemExit("Telegram authorization redirect is missing OIDC/PKCE parameters")
if query.get("code_challenge_method") != ["S256"]:
    raise SystemExit("Telegram authorization redirect does not require PKCE S256")
if query.get("redirect_uri") != [sys.argv[2]]:
    raise SystemExit("Telegram callback URL does not match the public QSDM Account origin")
PY
  jwks=$(curl --fail --silent --show-error --connect-timeout 5 --max-time 15 \
    https://oauth.telegram.org/.well-known/jwks.json)
  printf '%s' "$jwks" | python3 -c '
import json, sys
value = json.load(sys.stdin)
keys = value.get("keys") or []
if not any(item.get("kty") == "RSA" and item.get("alg") == "RS256" and item.get("kid") for item in keys):
    raise SystemExit("Telegram JWKS contains no usable RS256 signing key")
'
}

check_health "$local_url"
provider_flags=$(check_provider_config "$local_url")
read -r email_enabled telegram_enabled <<<"$provider_flags"
echo "Local QSDM Account service is healthy and has a login provider."

origin="${origin%/}"
[[ "$origin" == https://* ]] || {
  echo "Public origin must use HTTPS: $origin" >&2
  exit 1
}

probe_base="$local_url"
if [[ "$local_only" != true ]]; then
  page=$(curl --fail --silent --show-error --connect-timeout 5 --max-time 15 \
    "$origin/account/")
  grep -Fq '<title>QSDM Account</title>' <<<"$page" || {
    echo "Public account page did not return the QSDM Account document." >&2
    exit 1
  }
  check_health "$origin"
  public_provider_flags=$(check_provider_config "$origin")
  [[ "$public_provider_flags" == "$provider_flags" ]] || {
    echo "Public and local account provider reports do not match." >&2
    exit 1
  }
  check_public_security_headers "$origin/api/account/health"
  probe_base="$origin"
  echo "Public QSDM Account page, API, and security headers are healthy at $origin/account/."
fi

if [[ -n "$activation_email" ]]; then
  [[ "$email_enabled" == "1" ]] || {
    echo "Email activation was requested, but email sign-in is disabled." >&2
    exit 1
  }
  trigger_activation_email "$probe_base" "$activation_email"
  echo "Email provider accepted a real sign-in message for $activation_email. Consume the link from that inbox to finish the test."
fi

if [[ "$check_telegram" == true ]]; then
  [[ "$telegram_enabled" == "1" ]] || {
    echo "Telegram activation was requested, but Telegram sign-in is disabled." >&2
    exit 1
  }
  check_telegram_provider "$probe_base" "$origin"
  echo "Telegram authorization routing, PKCE parameters, callback URL, and signing keys are healthy. Complete one interactive Telegram login to finish the test."
fi
