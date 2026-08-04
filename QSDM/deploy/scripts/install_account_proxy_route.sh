#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: sudo install-account-proxy-route

Adds only the QSDM Account API route and account-page redirect to the live
Caddyfile. The command requires a healthy local account service, validates the
candidate Caddyfile, reloads Caddy, verifies the public route, and restores the
previous Caddyfile automatically if activation fails.
USAGE
}

if [[ ${1:-} == "-h" || ${1:-} == "--help" ]]; then
  usage
  exit 0
fi
if [[ $# -ne 0 ]]; then
  usage >&2
  exit 2
fi
if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "Run this installer as root." >&2
  exit 1
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
caddyfile_input=${QSDM_CADDYFILE:-/etc/caddy/Caddyfile}
default_merger=$script_dir/merge-account-caddy-route.py
default_verifier=$script_dir/verify-account-service
if [[ ! -f "$default_merger" ]]; then
  default_merger=$script_dir/merge_account_caddy_route.py
fi
if [[ ! -f "$default_verifier" ]]; then
  default_verifier=$script_dir/verify_account_service.sh
fi
merger=${QSDM_ACCOUNT_CADDY_MERGER:-$default_merger}
verifier=${QSDM_ACCOUNT_VERIFIER:-$default_verifier}
caddy_command=${QSDM_CADDY_COMMAND:-caddy}
systemctl_command=${QSDM_SYSTEMCTL_COMMAND:-systemctl}

for path in "$caddyfile_input" "$merger" "$verifier"; do
  if [[ ! -f "$path" ]]; then
    echo "Required file is missing: $path" >&2
    exit 1
  fi
done
for command_name in python3 realpath mktemp cmp install cp mv "$caddy_command" "$systemctl_command"; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "Required command is missing: $command_name" >&2
    exit 1
  }
done
caddyfile=$(realpath "$caddyfile_input")

# Never publish a route that points to a missing or provider-less service.
"$verifier" --local-only

# Keep the candidate beside the live file so any relative Caddy imports resolve
# exactly as they will after activation.
candidate=$(mktemp "${caddyfile}.account-merge.XXXXXXXX")
staged=""
backup=""
cleanup() {
  rm -f -- "$candidate"
  if [[ -n "$staged" ]]; then
    rm -f -- "$staged"
  fi
}
trap cleanup EXIT

python3 "$merger" "$caddyfile" "$candidate"
"$caddy_command" validate --config "$candidate" --adapter caddyfile

if cmp -s -- "$caddyfile" "$candidate"; then
  "$verifier"
  echo "QSDM Account proxy route is already installed and healthy."
  exit 0
fi

backup=$(mktemp "${caddyfile}.account-backup.XXXXXXXX")
cp --preserve=mode,ownership,timestamps -- "$caddyfile" "$backup"
staged=$(mktemp "${caddyfile}.account-candidate.XXXXXXXX")
install -m0644 -o root -g root -- "$candidate" "$staged"
mv -f -- "$staged" "$caddyfile"
staged=""

rollback() {
  local reason=$1
  local restored
  echo "$reason Restoring $backup." >&2
  restored=$(mktemp "${caddyfile}.account-restore.XXXXXXXX")
  cp --preserve=mode,ownership,timestamps -- "$backup" "$restored"
  mv -f -- "$restored" "$caddyfile"
  if ! "$systemctl_command" reload caddy.service; then
    echo "CRITICAL: the previous Caddyfile was restored, but Caddy reload failed." >&2
  fi
  exit 1
}

if ! "$systemctl_command" reload caddy.service; then
  rollback "Caddy rejected the activated account route."
fi
if ! "$verifier"; then
  rollback "Public QSDM Account verification failed after activation."
fi

echo "QSDM Account proxy route is active and healthy."
echo "Previous Caddyfile retained at $backup."
