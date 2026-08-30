#!/usr/bin/env bash

qsdm_derive_health_url_from_config() {
  local cfg="$1"
  local port=""
  local tls="false"
  if [[ -f "$cfg" ]]; then
    case "${cfg##*.}" in
      yaml|yml)
        port="$(awk '
          BEGIN { in_api=0 }
          /^[[:space:]]*api:[[:space:]]*$/ { in_api=1; next }
          /^[A-Za-z0-9_]+:[[:space:]]*$/ { in_api=0 }
          in_api && /^[[:space:]]*port:[[:space:]]*[0-9]+[[:space:]]*($|#)/ {
            sub(/^[[:space:]]*port:[[:space:]]*/, "")
            sub(/[[:space:]]*(#.*)?$/, "")
            print
            exit
          }
        ' "$cfg")"
        tls="$(awk '
          BEGIN { in_api=0 }
          /^[[:space:]]*api:[[:space:]]*$/ { in_api=1; next }
          /^[A-Za-z0-9_]+:[[:space:]]*$/ { in_api=0 }
          in_api && /^[[:space:]]*enable_tls:[[:space:]]*(true|false)[[:space:]]*($|#)/ {
            sub(/^[[:space:]]*enable_tls:[[:space:]]*/, "")
            sub(/[[:space:]]*(#.*)?$/, "")
            print
            exit
          }
        ' "$cfg")"
        ;;
      toml)
        port="$(awk '
          BEGIN { in_api=0 }
          /^[[:space:]]*\[api\][[:space:]]*$/ { in_api=1; next }
          /^[[:space:]]*\[/ { in_api=0 }
          in_api && /^[[:space:]]*port[[:space:]]*=[[:space:]]*[0-9]+[[:space:]]*($|#)/ {
            sub(/^[[:space:]]*port[[:space:]]*=[[:space:]]*/, "")
            sub(/[[:space:]]*(#.*)?$/, "")
            print
            exit
          }
        ' "$cfg")"
        tls="$(awk '
          BEGIN { in_api=0 }
          /^[[:space:]]*\[api\][[:space:]]*$/ { in_api=1; next }
          /^[[:space:]]*\[/ { in_api=0 }
          in_api && /^[[:space:]]*enable_tls[[:space:]]*=[[:space:]]*(true|false)[[:space:]]*($|#)/ {
            sub(/^[[:space:]]*enable_tls[[:space:]]*=[[:space:]]*/, "")
            sub(/[[:space:]]*(#.*)?$/, "")
            print
            exit
          }
        ' "$cfg")"
        ;;
    esac
  fi
  [[ "$port" =~ ^[1-9][0-9]*$ ]] || port="8080"
  if [[ "$tls" == "true" ]]; then
    printf 'https://127.0.0.1:%s/api/v1/health/ready\n' "$port"
  else
    printf 'http://127.0.0.1:%s/api/v1/health/ready\n' "$port"
  fi
}

qsdm_health_port_from_url() {
  local __out_var="$1"
  local url="$2"
  local rest=""
  case "$url" in
    http://*) rest="${url#http://}" ;;
    https://*) rest="${url#https://}" ;;
    *) return 1 ;;
  esac

  local port_path=""
  case "$rest" in
    127.0.0.1:*) port_path="${rest#127.0.0.1:}" ;;
    localhost:*) port_path="${rest#localhost:}" ;;
    \[::1\]:*) port_path="${rest#\[::1\]:}" ;;
    *) return 1 ;;
  esac

  local parsed_port="${port_path%%/*}"
  local path="/${port_path#*/}"
  if [[ "$port_path" == "$parsed_port" ]]; then
    path="/"
  fi
  [[ "$parsed_port" =~ ^[0-9]{1,5}$ ]] || return 1
  [[ "$path" =~ ^/[A-Za-z0-9._~/%-]*$ ]] || return 1
  parsed_port=$((10#$parsed_port))
  if (( parsed_port < 1 || parsed_port > 65535 )); then
    return 1
  fi
  printf -v "$__out_var" '%s' "$parsed_port"
  return 0
}