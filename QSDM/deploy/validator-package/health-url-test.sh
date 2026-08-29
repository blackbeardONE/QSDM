#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=health-url.sh
source "${SCRIPT_DIR}/health-url.sh"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

assert_eq() {
  local want="$1"
  local got="$2"
  local label="$3"
  if [[ "$got" != "$want" ]]; then
    printf 'FAIL %s\n  got:  %s\n  want: %s\n' "$label" "$got" "$want" >&2
    exit 1
  fi
}

cat >"$work/api-8443.yaml" <<'YAML'
api:
  port: 8443
  enable_tls: false
YAML
assert_eq 'http://127.0.0.1:8443/api/v1/health/ready' "$(qsdm_derive_health_url_from_config "$work/api-8443.yaml")" 'yaml api port'

cat >"$work/api-tls.toml" <<'TOML'
[api]
port = 9443
enable_tls = true
TOML
assert_eq 'https://127.0.0.1:9443/api/v1/health/ready' "$(qsdm_derive_health_url_from_config "$work/api-tls.toml")" 'toml tls port'

cat >"$work/no-api.yaml" <<'YAML'
network:
  port: 4001
YAML
assert_eq 'http://127.0.0.1:8080/api/v1/health/ready' "$(qsdm_derive_health_url_from_config "$work/no-api.yaml")" 'fallback port'

port=''
qsdm_health_port_from_url port 'http://localhost:8443/api/v1/health/ready'
assert_eq '8443' "$port" 'loopback port parse'

if qsdm_health_port_from_url port 'https://api.qsdm.tech/api/v1/health/ready'; then
  printf 'FAIL public health URL should not be accepted for installer listener ownership checks\n' >&2
  exit 1
fi

printf 'validator package health URL tests passed\n'