#!/usr/bin/env bash

# Helpers for selecting one explicit Caddy site block without baking a
# production hostname into deployment scripts.

qsdm_normalize_caddy_site_labels() {
  local raw="${1-}"
  local label normalized=""
  local -a labels

  raw="${raw//$'\r'/}"
  IFS=',' read -r -a labels <<<"${raw}"
  if [[ ${#labels[@]} -eq 0 ]]; then
    echo "at least one Caddy site label is required" >&2
    return 64
  fi

  for label in "${labels[@]}"; do
    label="${label#"${label%%[![:space:]]*}"}"
    label="${label%"${label##*[![:space:]]}"}"
    label="${label,,}"
    if [[ -z "${label}" || ! "${label}" =~ ^[A-Za-z0-9.-]+(:[0-9]{1,5})?$ ]]; then
      echo "invalid Caddy site label: ${label:-<empty>}" >&2
      return 64
    fi
    if [[ "${label}" == .* || "${label}" == *..* || "${label}" == *.-* || "${label}" == *-. ]]; then
      echo "invalid Caddy site label: ${label}" >&2
      return 64
    fi
    if [[ ",${normalized}," == *",${label},"* ]]; then
      continue
    fi
    if [[ -n "${normalized}" ]]; then
      normalized+=", "
    fi
    normalized+="${label}"
  done

  [[ -n "${normalized}" ]] || return 64
  printf '%s\n' "${normalized}"
}

qsdm_caddy_site_has_import() {
  local caddyfile="$1"
  local expected_labels="$2"
  local import_line="${3:-import /etc/caddy/qsdm-edge-relay.caddy}"

  awk -v expected_labels="${expected_labels}" -v import_line="${import_line}" '
    function trim(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      return value
    }
    function labels_from_site_line(value, pieces, count, part, label, normalized) {
      sub(/[[:space:]]*\{[[:space:]]*$/, "", value)
      count = split(value, pieces, ",")
      normalized = ""
      for (part = 1; part <= count; part++) {
        label = tolower(trim(pieces[part]))
        if (label == "") {
          continue
        }
        if (normalized != "") {
          normalized = normalized ", "
        }
        normalized = normalized label
      }
      return normalized
    }
    function brace_delta(value, copy, opens, closes) {
      copy = value
      opens = gsub(/\{/, "{", copy)
      copy = value
      closes = gsub(/\}/, "}", copy)
      return opens - closes
    }
    BEGIN { in_site = 0; depth = 0; found = 0 }
    {
      if (!in_site) {
        if ($0 ~ /\{[[:space:]]*$/ && labels_from_site_line($0) == expected_labels) {
          in_site = 1
          depth = 1
        }
        next
      }
      if (index($0, import_line) > 0) {
        found = 1
      }
      depth += brace_delta($0)
      if (depth <= 0) {
        exit
      }
    }
    END { exit(found ? 0 : 1) }
  ' "${caddyfile}"
}

qsdm_patch_caddy_site_import() {
  local caddyfile="$1"
  local output="$2"
  local expected_labels="$3"
  local import_line="${4:-import /etc/caddy/qsdm-edge-relay.caddy}"

  awk -v expected_labels="${expected_labels}" -v import_line="${import_line}" '
    function trim(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      return value
    }
    function labels_from_site_line(value, pieces, count, part, label, normalized) {
      sub(/[[:space:]]*\{[[:space:]]*$/, "", value)
      count = split(value, pieces, ",")
      normalized = ""
      for (part = 1; part <= count; part++) {
        label = tolower(trim(pieces[part]))
        if (label == "") {
          continue
        }
        if (normalized != "") {
          normalized = normalized ", "
        }
        normalized = normalized label
      }
      return normalized
    }
    {
      print
      if (!inserted && $0 ~ /\{[[:space:]]*$/ && labels_from_site_line($0) == expected_labels) {
        print "\t" import_line
        inserted = 1
      }
    }
    END { if (!inserted) exit 42 }
  ' "${caddyfile}" >"${output}"
}
