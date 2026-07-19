#!/usr/bin/env bash

set -euo pipefail

INSTALL_DIR="/opt/qsdm"
SERVICE_NAME="qsdm"
SERVICE_EXPLICIT=0
HEALTH_URL="http://127.0.0.1:8080/api/v1/health/live"
HEALTH_EXPLICIT=0
HEALTH_TIMEOUT=120

die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-dir) INSTALL_DIR="${2:-}"; shift 2 ;;
    --service) SERVICE_NAME="${2:-}"; SERVICE_EXPLICIT=1; shift 2 ;;
    --health-url) HEALTH_URL="${2:-}"; HEALTH_EXPLICIT=1; shift 2 ;;
    --health-timeout) HEALTH_TIMEOUT="${2:-}"; shift 2 ;;
    -h|--help)
      echo "Usage: sudo ./rollback.sh [--install-dir PATH] [--service NAME] [--health-url URL]"
      exit 0
      ;;
    *) die "unknown option: $1" ;;
  esac
done

[[ "$(id -u)" -eq 0 ]] || die "run this script as root (sudo)"
[[ "$INSTALL_DIR" == /* && "$INSTALL_DIR" != "/" && ! -L "$INSTALL_DIR" ]] || die "unsafe install directory"
[[ "$SERVICE_NAME" =~ ^[A-Za-z0-9_.@-]+$ ]] || die "invalid service name"
INSTALL_DIR="$(cd "$INSTALL_DIR" && pwd -P)"
[[ "$(stat -c '%u' -- "$INSTALL_DIR")" -eq 0 ]] || die "install directory must be owned by root"
[[ -z "$(find "$INSTALL_DIR" -maxdepth 0 -perm /022 -print -quit)" ]] || die "install directory must not be group/other writable"
STATE_FILE="$INSTALL_DIR/validator-install-state"
TARGET_BINARY="$INSTALL_DIR/qsdm"
[[ -f "$STATE_FILE" && ! -L "$STATE_FILE" ]] || die "install state is missing: $STATE_FILE"
[[ -f "$TARGET_BINARY" && ! -L "$TARGET_BINARY" ]] || die "installed validator binary is missing or is a symlink"
[[ "$(stat -c '%u' -- "$STATE_FILE")" -eq 0 ]] || die "install state must be owned by root"
[[ -z "$(find "$STATE_FILE" -maxdepth 0 -perm /077 -print -quit)" ]] || die "install state permissions must be 0600 or stricter"
[[ "$(stat -c '%u' -- "$TARGET_BINARY")" -eq 0 ]] || die "installed binary must be owned by root"
[[ -z "$(find "$TARGET_BINARY" -maxdepth 0 -perm /022 -print -quit)" ]] || die "installed binary must not be group/other writable"
[[ "$HEALTH_TIMEOUT" =~ ^[1-9][0-9]*$ ]] || die "--health-timeout must be a positive integer"

if [[ "$SERVICE_EXPLICIT" -eq 0 ]]; then
  recorded_service="$(awk -F= '$1 == "service" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
  [[ -z "$recorded_service" ]] || SERVICE_NAME="$recorded_service"
  [[ "$SERVICE_NAME" =~ ^[A-Za-z0-9_.@-]+$ ]] || die "invalid service name in install state"
fi
if [[ "$HEALTH_EXPLICIT" -eq 0 ]]; then
  recorded_health="$(awk -F= '$1 == "health" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
  [[ -z "$recorded_health" ]] || HEALTH_URL="$recorded_health"
fi
if [[ "$HEALTH_URL" =~ ^https?://(127\.0\.0\.1|localhost|\[::1\]):([0-9]{1,5})(/[A-Za-z0-9._~/%-]*)?$ ]]; then
  HEALTH_PORT=$((10#${BASH_REMATCH[2]}))
else
  die "--health-url must be an explicit loopback HTTP(S) URL with a port"
fi
(( HEALTH_PORT >= 1 && HEALTH_PORT <= 65535 )) || die "--health-url port is out of range"
service_user="$(awk -F= '$1 == "user" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
data_dir="$(awk -F= '$1 == "data" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
[[ "$service_user" =~ ^[A-Za-z_][A-Za-z0-9_-]*$ ]] || die "invalid service user in install state"
[[ "$data_dir" == /* && "$data_dir" != "/" && "$data_dir" =~ ^/[A-Za-z0-9._/-]+$ ]] ||
  die "invalid data directory in install state"
data_dir="$(realpath -m -- "$data_dir")"
systemctl cat "${SERVICE_NAME}.service" >/dev/null 2>&1 || die "validator service is missing"
[[ "$(systemctl show "${SERVICE_NAME}.service" --property User --value)" == "$service_user" ]] ||
  die "service user does not match QSDM install state"
[[ "$(systemctl show "${SERVICE_NAME}.service" --property WorkingDirectory --value)" == "$data_dir" ]] ||
  die "service working directory does not match QSDM install state"

backup="$(awk -F= '$1 == "previous" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
[[ -n "$backup" && -f "$backup" && ! -L "$backup" ]] || die "no valid previous binary is recorded"
case "$(cd "$(dirname "$backup")" && pwd -P)/$(basename "$backup")" in
  "$INSTALL_DIR"/*) ;;
  *) die "recorded backup is outside the install directory" ;;
esac
[[ "$(stat -c '%u' -- "$backup")" -eq 0 ]] || die "recorded backup must be owned by root"
[[ -z "$(find "$backup" -maxdepth 0 -perm /022 -print -quit)" ]] || die "recorded backup must not be group/other writable"
backup_hash="$(awk -F= '$1 == "previous_sha256" {print tolower(substr($0, index($0, "=") + 1)); exit}' "$STATE_FILE")"
[[ "$backup_hash" =~ ^[0-9a-f]{64}$ ]] || die "no valid previous binary checksum is recorded"
[[ "$(sha256sum "$backup" | awk '{print tolower($1)}')" == "$backup_hash" ]] || die "previous validator binary checksum mismatch"

"$backup" --version | grep -q '^qsdm ' || die "backup does not provide canonical version metadata"
command -v curl >/dev/null 2>&1 || die "curl is required for the liveness check"
command -v ss >/dev/null 2>&1 || die "ss is required to verify the API listener owner"
failed="$INSTALL_DIR/qsdm.failed.$(date -u +%Y%m%dT%H%M%SZ).${BASHPID}"
was_active=0
if systemctl is-active --quiet "${SERVICE_NAME}.service"; then
  was_active=1
fi
rollback_applied=0
rollback_started=0

restore_failed_rollback() {
  local status=$?
  if [[ "$status" -ne 0 && "$rollback_started" -eq 1 ]]; then
    set +e
    printf 'Rollback failed; restoring the validator that was active before rollback.\n' >&2
    if [[ "$rollback_applied" -eq 1 ]]; then
      systemctl stop "${SERVICE_NAME}.service" >/dev/null 2>&1
      cp --preserve=mode,timestamps "$failed" "$TARGET_BINARY"
    fi
    if [[ "$was_active" -eq 1 ]]; then
      systemctl start "${SERVICE_NAME}.service" >/dev/null 2>&1
    fi
    set -e
  fi
  exit "$status"
}
trap restore_failed_rollback EXIT

rollback_started=1
systemctl stop "${SERVICE_NAME}.service"
cp --preserve=mode,timestamps "$TARGET_BINARY" "$failed"
rollback_applied=1
cp --preserve=mode,timestamps "$backup" "$TARGET_BINARY"
systemctl start "${SERVICE_NAME}.service"

deadline=$((SECONDS + HEALTH_TIMEOUT))
while (( SECONDS < deadline )); do
  if curl --fail --silent --show-error --max-time 5 "$HEALTH_URL" >/dev/null 2>&1; then
    main_pid="$(systemctl show "${SERVICE_NAME}.service" --property MainPID --value 2>/dev/null || true)"
    runtime_user="$(systemctl show "${SERVICE_NAME}.service" --property User --value 2>/dev/null || true)"
    listener="$(ss -H -ltnp "sport = :${HEALTH_PORT}" 2>/dev/null || true)"
    if [[ ! "$main_pid" =~ ^[1-9][0-9]*$ ]] ||
      [[ "$runtime_user" != "$service_user" ]] ||
      [[ "$(readlink -f "/proc/${main_pid}/exe" 2>/dev/null || true)" != "$TARGET_BINARY" ]] ||
      ! grep -Fq "pid=${main_pid}," <<<"$listener"; then
      sleep 2
      continue
    fi
    config="$(awk -F= '$1 == "config" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
    failed_hash="$(sha256sum "$failed" | awk '{print tolower($1)}')"
    state_tmp="$STATE_FILE.tmp.$$"
    {
      printf 'status=installed\n'
      printf 'version=%s\n' "$($TARGET_BINARY --version)"
      printf 'config=%s\n' "$config"
      printf 'binary=%s\n' "$TARGET_BINARY"
      printf 'previous=%s\n' "$failed"
      printf 'previous_sha256=%s\n' "$failed_hash"
      printf 'service=%s\n' "$SERVICE_NAME"
      printf 'user=%s\n' "$service_user"
      printf 'data=%s\n' "$data_dir"
      printf 'health=%s\n' "$HEALTH_URL"
      printf 'installed_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    } >"$state_tmp"
    chmod 0600 "$state_tmp"
    mv -f "$state_tmp" "$STATE_FILE"
    printf 'Rollback complete: %s\n' "$($TARGET_BINARY --version)"
    printf 'Replaced build preserved at %s\n' "$failed"
    exit 0
  fi
  sleep 2
done
die "rollback binary was restored but did not become live within ${HEALTH_TIMEOUT}s"
