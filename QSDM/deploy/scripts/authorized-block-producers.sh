#!/usr/bin/env bash
# Shared parsing for the producer allowlist rendered by the validator installers.
#
# The chain deliberately accepts opaque producer identifiers because a deployed
# network may use either a peer ID or a consensus-derived producer address.
# This helper therefore validates transport safety (not a particular ID format),
# removes accidental duplicates, and emits TOML with correct escaping.

qsdm_normalize_bool() {
  local label="$1"
  local raw="$2"

  case "$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on) printf 'true\n' ;;
    0|false|no|off|"") printf 'false\n' ;;
    *)
      printf 'Invalid %s=%s; use true or false\n' "$label" "$raw" >&2
      return 64
      ;;
  esac
}

qsdm_authorized_block_producer_lines() {
  local raw="$1"
  local part trimmed
  local -A seen=()
  local -a parts=()

  if [[ "$raw" == *$'\n'* || "$raw" == *$'\r'* ]]; then
    printf 'authorized block producer IDs must not contain control characters\n' >&2
    return 64
  fi

  IFS=',' read -r -a parts <<< "$raw"
  for part in "${parts[@]}"; do
    trimmed="$part"
    trimmed="${trimmed#"${trimmed%%[![:space:]]*}"}"
    trimmed="${trimmed%"${trimmed##*[![:space:]]}"}"
    [[ -n "$trimmed" ]] || continue
    if [[ ! "$trimmed" =~ ^[A-Za-z0-9._:-]+$ ]]; then
      printf 'authorized block producer IDs must contain only letters, digits, dot, underscore, colon, or hyphen\n' >&2
      return 64
    fi
    if [[ -z "${seen[$trimmed]+x}" ]]; then
      seen["$trimmed"]=1
      printf '%s\n' "$trimmed"
    fi
  done
}

qsdm_authorized_block_producer_count() {
  local count=0
  local producer lines

  lines="$(qsdm_authorized_block_producer_lines "$1")" || return $?
  while IFS= read -r producer; do
    [[ -n "$producer" ]] && ((count += 1))
  done <<< "$lines"
  printf '%s\n' "$count"
}

qsdm_render_authorized_block_producers_toml() {
  local raw="$1"
  local -a producers=()
  local producer escaped index lines

  lines="$(qsdm_authorized_block_producer_lines "$raw")" || return $?
  while IFS= read -r producer; do
    [[ -n "$producer" ]] && producers+=("$producer")
  done <<< "$lines"

  printf 'authorized_block_producers = ['
  if ((${#producers[@]} == 0)); then
    printf ']\n'
    return 0
  fi
  printf '\n'
  for index in "${!producers[@]}"; do
    escaped="${producers[$index]//\\/\\\\}"
    escaped="${escaped//\"/\\\"}"
    if ((index + 1 < ${#producers[@]})); then
      printf '  "%s",\n' "$escaped"
    else
      printf '  "%s"\n' "$escaped"
    fi
  done
  printf ']\n'
}
