#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
SOURCE_BINARY="${SCRIPT_DIR}/qsdm-validator"
CHECKSUM_FILE="${SCRIPT_DIR}/SHA256SUMS.txt"
INSTALL_DIR="/opt/qsdm"
DATA_DIR="/var/lib/qsdm"
DATA_EXPLICIT=0
CONFIG_SOURCE=""
SERVICE_NAME="qsdm"
SERVICE_EXPLICIT=0
SERVICE_USER="qsdm"
USER_EXPLICIT=0
HEALTH_URL="http://127.0.0.1:8080/api/v1/health/live"
HEALTH_EXPLICIT=0
HEALTH_TIMEOUT=120
NO_START=0

usage() {
  cat <<'EOF'
Install or atomically update a QSDM validator.

Usage:
  sudo ./install-or-update.sh [options]

Options:
  --install-dir PATH       Binary/config directory (default: /opt/qsdm)
  --data-dir PATH          Writable runtime directory (default: /var/lib/qsdm)
  --config PATH            TOML/YAML config. Required for a fresh install.
  --service NAME           systemd unit name without .service (default: qsdm)
  --user NAME              service account for a fresh install (default: qsdm)
  --health-url URL         liveness URL (default: local API port 8080)
  --health-timeout SEC     rollback deadline after restart (default: 120)
  --no-start               install/update without starting the service
  -h, --help               show this help

The script never modifies validator databases, journals, wallets, or keys.
EOF
}

die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-dir) INSTALL_DIR="${2:-}"; shift 2 ;;
    --data-dir) DATA_DIR="${2:-}"; DATA_EXPLICIT=1; shift 2 ;;
    --config) CONFIG_SOURCE="${2:-}"; shift 2 ;;
    --service) SERVICE_NAME="${2:-}"; SERVICE_EXPLICIT=1; shift 2 ;;
    --user) SERVICE_USER="${2:-}"; USER_EXPLICIT=1; shift 2 ;;
    --health-url) HEALTH_URL="${2:-}"; HEALTH_EXPLICIT=1; shift 2 ;;
    --health-timeout) HEALTH_TIMEOUT="${2:-}"; shift 2 ;;
    --no-start) NO_START=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

[[ "$(id -u)" -eq 0 ]] || die "run this script as root (sudo)"
[[ "$INSTALL_DIR" == /* && "$INSTALL_DIR" != "/" ]] || die "--install-dir must be an absolute non-root path"
[[ "$INSTALL_DIR" != *$'\n'* && "$INSTALL_DIR" != *$'\r'* ]] || die "--install-dir contains a newline"
[[ "$INSTALL_DIR" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "--install-dir contains unsupported characters"
[[ "$SERVICE_NAME" =~ ^[A-Za-z0-9_.@-]+$ ]] || die "invalid --service name"
[[ "$SERVICE_USER" =~ ^[A-Za-z_][A-Za-z0-9_-]*$ ]] || die "invalid --user name"
[[ "$HEALTH_TIMEOUT" =~ ^[1-9][0-9]*$ ]] || die "--health-timeout must be a positive integer"
[[ -f "$SOURCE_BINARY" && ! -L "$SOURCE_BINARY" ]] || die "package binary is missing or is a symlink: $SOURCE_BINARY"
[[ -f "$CHECKSUM_FILE" && ! -L "$CHECKSUM_FILE" ]] || die "package checksum file is missing or is a symlink"

expected_hash="$(awk '$2 == "qsdm-validator" || $2 == "*qsdm-validator" {print tolower($1); exit}' "$CHECKSUM_FILE")"
[[ "$expected_hash" =~ ^[0-9a-f]{64}$ ]] || die "SHA256SUMS.txt has no valid qsdm-validator entry"
actual_hash="$(sha256sum "$SOURCE_BINARY" | awk '{print tolower($1)}')"
[[ "$actual_hash" == "$expected_hash" ]] || die "package binary checksum mismatch"

version_output="$($SOURCE_BINARY --version)"
[[ "$version_output" == qsdm\ * ]] || die "package binary did not return canonical qsdm version metadata"
printf 'Verified package: %s\n' "$version_output"

if [[ -e "$INSTALL_DIR" && -L "$INSTALL_DIR" ]]; then
  die "refusing a symlink install directory: $INSTALL_DIR"
fi
if [[ -e "$INSTALL_DIR" ]]; then
  [[ -d "$INSTALL_DIR" ]] || die "install path is not a directory: $INSTALL_DIR"
  [[ "$(stat -c '%u' -- "$INSTALL_DIR")" -eq 0 ]] || die "install directory must be owned by root"
  [[ -z "$(find "$INSTALL_DIR" -maxdepth 0 -perm /022 -print -quit)" ]] || die "install directory must not be group/other writable"
else
  install -d -m 0750 -o root -g root "$INSTALL_DIR"
fi
INSTALL_DIR="$(cd "$INSTALL_DIR" && pwd -P)"
TARGET_BINARY="$INSTALL_DIR/qsdm"
STATE_FILE="$INSTALL_DIR/validator-install-state"
STATE_WAS_PRESENT=0
if [[ ! -f "$STATE_FILE" ]] && [[ -n "$(find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
  die "refusing to adopt a non-empty install directory without QSDM install state"
fi

previous_config=""
recorded_data=""
if [[ -e "$STATE_FILE" && ( ! -f "$STATE_FILE" || -L "$STATE_FILE" ) ]]; then
  die "refusing invalid install state: $STATE_FILE"
fi
if [[ -f "$STATE_FILE" ]]; then
  STATE_WAS_PRESENT=1
  [[ "$(stat -c '%u' -- "$STATE_FILE")" -eq 0 ]] || die "install state must be owned by root"
  [[ -z "$(find "$STATE_FILE" -maxdepth 0 -perm /077 -print -quit)" ]] || die "install state permissions must be 0600 or stricter"
  previous_config="$(awk -F= '$1 == "config" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
  if [[ "$SERVICE_EXPLICIT" -eq 0 ]]; then
    recorded_service="$(awk -F= '$1 == "service" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
    [[ -z "$recorded_service" ]] || SERVICE_NAME="$recorded_service"
  fi
  if [[ "$USER_EXPLICIT" -eq 0 ]]; then
    recorded_user="$(awk -F= '$1 == "user" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
    [[ -z "$recorded_user" ]] || SERVICE_USER="$recorded_user"
  fi
  if [[ "$HEALTH_EXPLICIT" -eq 0 ]]; then
    recorded_health="$(awk -F= '$1 == "health" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
    [[ -z "$recorded_health" ]] || HEALTH_URL="$recorded_health"
  fi
  recorded_data="$(awk -F= '$1 == "data" {print substr($0, index($0, "=") + 1); exit}' "$STATE_FILE")"
  if [[ "$DATA_EXPLICIT" -eq 0 ]]; then
    [[ -z "$recorded_data" ]] || DATA_DIR="$recorded_data"
  fi
fi
[[ "$SERVICE_NAME" =~ ^[A-Za-z0-9_.@-]+$ ]] || die "invalid service name in install state"
[[ "$SERVICE_USER" =~ ^[A-Za-z_][A-Za-z0-9_-]*$ ]] || die "invalid service user in install state"
[[ "$DATA_DIR" == /* && "$DATA_DIR" != "/" ]] || die "--data-dir must be an absolute non-root path"
[[ "$DATA_DIR" != *$'\n'* && "$DATA_DIR" != *$'\r'* ]] || die "--data-dir contains a newline"
[[ "$DATA_DIR" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "--data-dir contains unsupported characters"
[[ ! -L "$DATA_DIR" ]] || die "refusing a symlink data directory: $DATA_DIR"
DATA_DIR="$(realpath -m -- "$DATA_DIR")"
[[ "$DATA_DIR" != "$INSTALL_DIR" ]] || die "--data-dir must differ from the root-managed install directory"
case "$INSTALL_DIR/" in
  "$DATA_DIR/"*) die "--data-dir cannot be an ancestor of the root-managed install directory" ;;
esac
case "$DATA_DIR/" in
  "$INSTALL_DIR/"*) die "--data-dir cannot be inside the root-managed install directory" ;;
esac
if [[ "$HEALTH_URL" =~ ^https?://(127\.0\.0\.1|localhost|\[::1\]):([0-9]{1,5})(/[A-Za-z0-9._~/%-]*)?$ ]]; then
  HEALTH_PORT=$((10#${BASH_REMATCH[2]}))
else
  die "--health-url must be an explicit loopback HTTP(S) URL with a port"
fi
(( HEALTH_PORT >= 1 && HEALTH_PORT <= 65535 )) || die "--health-url port is out of range"
UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
if systemctl cat "${SERVICE_NAME}.service" >/dev/null 2>&1 && [[ ! -f "$STATE_FILE" ]]; then
  die "refusing to adopt an existing service without QSDM install state"
fi

if [[ -n "$CONFIG_SOURCE" ]]; then
  [[ "$CONFIG_SOURCE" == /* ]] || CONFIG_SOURCE="$(cd "$(dirname "$CONFIG_SOURCE")" && pwd -P)/$(basename "$CONFIG_SOURCE")"
  [[ -f "$CONFIG_SOURCE" && ! -L "$CONFIG_SOURCE" ]] || die "--config is missing or is a symlink: $CONFIG_SOURCE"
  case "${CONFIG_SOURCE##*.}" in
    toml|yaml|yml) ;;
    *) die "--config must end in .toml, .yaml, or .yml" ;;
  esac
  CONFIG_TARGET="$INSTALL_DIR/qsdm.${CONFIG_SOURCE##*.}"
elif [[ -n "$previous_config" && -f "$previous_config" ]]; then
  CONFIG_TARGET="$previous_config"
else
  CONFIG_TARGET=""
  for candidate in "$INSTALL_DIR/qsdm.toml" "$INSTALL_DIR/qsdm.yaml" "$INSTALL_DIR/qsdm.yml"; do
    if [[ -f "$candidate" ]]; then
      CONFIG_TARGET="$candidate"
      break
    fi
  done
  [[ -n "$CONFIG_TARGET" ]] || die "fresh install requires --config PATH"
fi
[[ "$CONFIG_TARGET" == /* && "$CONFIG_TARGET" =~ ^/[A-Za-z0-9._/-]+$ ]] || die "validator config path contains unsupported characters"
if [[ -e "$CONFIG_TARGET" && ( ! -f "$CONFIG_TARGET" || -L "$CONFIG_TARGET" ) ]]; then
  die "validator config is not a regular file: $CONFIG_TARGET"
fi
if [[ -z "$CONFIG_SOURCE" ]]; then
  [[ -f "$CONFIG_TARGET" ]] || die "validator config is missing: $CONFIG_TARGET"
fi

if ! getent passwd "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --user-group --home-dir "$INSTALL_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
fi
[[ "$(id -u "$SERVICE_USER")" -ne 0 ]] || die "the validator service account must not be root"
getent group "$SERVICE_USER" >/dev/null 2>&1 || die "the validator service account requires a same-named group"
chown "root:$SERVICE_USER" "$INSTALL_DIR"
chmod 0750 "$INSTALL_DIR"
if [[ -e "$DATA_DIR" ]]; then
  [[ -d "$DATA_DIR" && ! -L "$DATA_DIR" ]] || die "refusing a non-directory or symlink data path: $DATA_DIR"
  recorded_data_canonical=""
  [[ -z "$recorded_data" ]] || recorded_data_canonical="$(realpath -m -- "$recorded_data")"
  if [[ "$recorded_data_canonical" != "$DATA_DIR" ]] &&
    [[ -n "$(find "$DATA_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    die "refusing to adopt a non-empty unmanaged data directory: $DATA_DIR"
  fi
  if [[ -n "$(find "$DATA_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    [[ "$(stat -c '%u' -- "$DATA_DIR")" -eq "$(id -u "$SERVICE_USER")" ]] ||
      die "managed data directory is not owned by the service user"
  fi
fi

backup=""
backup_hash=""
write_state() {
  local install_status="${1:-installed}"
  local state_tmp="$STATE_FILE.tmp.$$"
  {
    printf 'status=%s\n' "$install_status"
    printf 'version=%s\n' "$version_output"
    printf 'config=%s\n' "$CONFIG_TARGET"
    printf 'binary=%s\n' "$TARGET_BINARY"
    printf 'previous=%s\n' "$backup"
    printf 'previous_sha256=%s\n' "$backup_hash"
    printf 'service=%s\n' "$SERVICE_NAME"
    printf 'user=%s\n' "$SERVICE_USER"
    printf 'data=%s\n' "$DATA_DIR"
    printf 'health=%s\n' "$HEALTH_URL"
    printf 'installed_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } >"$state_tmp"
  chmod 0600 "$state_tmp"
  mv -f "$state_tmp" "$STATE_FILE"
}

# Mark a fresh directory as installer-managed before creating writable data or
# copying configuration. If the host loses power mid-install, a later run can
# safely resume without adopting an unrelated non-empty directory.
if [[ "$STATE_WAS_PRESENT" -eq 0 ]]; then
  write_state installing
fi
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" "$DATA_DIR"
DATA_DIR="$(cd "$DATA_DIR" && pwd -P)"

if [[ -n "$CONFIG_SOURCE" ]]; then
  if [[ -e "$CONFIG_TARGET" && ! "$CONFIG_SOURCE" -ef "$CONFIG_TARGET" ]]; then
    die "refusing to overwrite existing config: $CONFIG_TARGET"
  fi
  if [[ ! -e "$CONFIG_TARGET" ]]; then
    install -m 0640 -o root -g "$SERVICE_USER" "$CONFIG_SOURCE" "$CONFIG_TARGET"
  fi
fi
[[ -f "$CONFIG_TARGET" && ! -L "$CONFIG_TARGET" ]] || die "validator config installation failed: $CONFIG_TARGET"

staged="$INSTALL_DIR/.qsdm.staged.$$"
install -m 0755 -o root -g root "$SOURCE_BINARY" "$staged"
"$staged" --version >/dev/null

timestamp="$(date -u +%Y%m%dT%H%M%SZ).${BASHPID}"
if [[ -e "$TARGET_BINARY" ]]; then
  [[ -f "$TARGET_BINARY" && ! -L "$TARGET_BINARY" ]] || die "refusing to replace non-regular target: $TARGET_BINARY"
  [[ "$(stat -c '%u' -- "$TARGET_BINARY")" -eq 0 ]] || die "installed binary must be owned by root"
  [[ -z "$(find "$TARGET_BINARY" -maxdepth 0 -perm /022 -print -quit)" ]] || die "installed binary must not be group/other writable"
  backup="$INSTALL_DIR/qsdm.backup.${timestamp}"
  cp --preserve=mode,timestamps "$TARGET_BINARY" "$backup"
  backup_hash="$(sha256sum "$backup" | awk '{print tolower($1)}')"
fi

unit_existed=0
was_active=0
unit_created=0
replacement_installed=0
transaction_started=0

restore_after_failure() {
  local status=$?
  set +e
  rm -f "$staged"
  if [[ "$status" -ne 0 && "$transaction_started" -eq 1 ]]; then
    printf 'Installation failed; restoring the previous validator state.\n' >&2
    systemctl stop "${SERVICE_NAME}.service" >/dev/null 2>&1
    if [[ "$replacement_installed" -eq 1 ]]; then
      if [[ -n "$backup" && -f "$backup" ]]; then
        cp --preserve=mode,timestamps "$backup" "$TARGET_BINARY"
      else
        rm -f "$TARGET_BINARY"
      fi
    fi
    if [[ "$unit_created" -eq 1 ]]; then
      systemctl disable "${SERVICE_NAME}.service" >/dev/null 2>&1
      rm -f "$UNIT_PATH"
      systemctl daemon-reload >/dev/null 2>&1
    elif [[ "$was_active" -eq 1 ]]; then
      systemctl start "${SERVICE_NAME}.service" >/dev/null 2>&1
    fi
  fi
  exit "$status"
}
trap restore_after_failure EXIT

if systemctl cat "${SERVICE_NAME}.service" >/dev/null 2>&1; then
  [[ "$(systemctl show "${SERVICE_NAME}.service" --property User --value)" == "$SERVICE_USER" ]] ||
    die "existing service user does not match QSDM install state"
  [[ "$(systemctl show "${SERVICE_NAME}.service" --property WorkingDirectory --value)" == "$DATA_DIR" ]] ||
    die "existing service working directory does not match QSDM install state"
  unit_existed=1
  if systemctl is-active --quiet "${SERVICE_NAME}.service"; then
    was_active=1
  fi
fi

transaction_started=1
if [[ "$unit_existed" -eq 1 ]]; then
  systemctl stop "${SERVICE_NAME}.service"
fi
mv -f "$staged" "$TARGET_BINARY"
replacement_installed=1

if [[ "$unit_existed" -eq 0 ]]; then
  cat >"$UNIT_PATH" <<EOF
[Unit]
Description=QSDM validator
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
WorkingDirectory=${DATA_DIR}
Environment="CONFIG_FILE=${CONFIG_TARGET}"
Environment="QSDM_PRODUCTION_MODE=1"
Environment="QSDM_REQUIRE_SQLITE_STORAGE=1"
ExecStart=${TARGET_BINARY}
Restart=always
RestartSec=10
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${DATA_DIR}
UMask=0077
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
  unit_created=1
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}.service"
fi

if [[ "$NO_START" -eq 0 ]]; then
  command -v curl >/dev/null 2>&1 || die "curl is required for the liveness check"
  command -v ss >/dev/null 2>&1 || die "ss is required to verify the API listener owner"
  systemctl start "${SERVICE_NAME}.service"
  deadline=$((SECONDS + HEALTH_TIMEOUT))
  healthy=0
  while (( SECONDS < deadline )); do
    if curl --fail --silent --show-error --max-time 5 "$HEALTH_URL" >/dev/null 2>&1; then
      main_pid="$(systemctl show "${SERVICE_NAME}.service" --property MainPID --value 2>/dev/null || true)"
      runtime_user="$(systemctl show "${SERVICE_NAME}.service" --property User --value 2>/dev/null || true)"
      listener="$(ss -H -ltnp "sport = :${HEALTH_PORT}" 2>/dev/null || true)"
      if [[ "$main_pid" =~ ^[1-9][0-9]*$ ]] &&
        [[ "$runtime_user" == "$SERVICE_USER" ]] &&
        [[ "$(readlink -f "/proc/${main_pid}/exe" 2>/dev/null || true)" == "$TARGET_BINARY" ]] &&
        grep -Fq "pid=${main_pid}," <<<"$listener"; then
        healthy=1
        break
      fi
    fi
    sleep 2
  done
  if [[ "$healthy" -ne 1 ]]; then
    die "validator did not become live within ${HEALTH_TIMEOUT}s"
  fi
  write_state installed
elif [[ "$was_active" -eq 1 ]]; then
  printf 'Service was active and is now stopped because --no-start was requested.\n'
  write_state installed
else
  write_state installed
fi

transaction_started=0
trap - EXIT
printf 'Installed %s at %s\n' "$version_output" "$TARGET_BINARY"
[[ -z "$backup" ]] || printf 'Rollback copy: %s\n' "$backup"
