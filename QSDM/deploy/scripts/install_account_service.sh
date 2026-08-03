#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: sudo install_account_service.sh BINARY CONFIG

Installs the qsdm-account binary, its hardened systemd unit, and a completed
private environment file. The script refuses example placeholders.
USAGE
}

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "Run this installer as root." >&2
  exit 1
fi
if [[ $# -ne 2 ]]; then
  usage >&2
  exit 2
fi

binary=$(realpath "$1")
config=$(realpath "$2")
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
unit=$(realpath "$script_dir/../systemd/qsdm-account.service")
verifier=$(realpath "$script_dir/verify_account_service.sh")

for path in "$binary" "$config" "$unit" "$verifier"; do
  if [[ ! -f "$path" ]]; then
    echo "Required file is missing: $path" >&2
    exit 1
  fi
done
for command_name in systemctl systemd-run; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "Required command is missing: $command_name" >&2
    exit 1
  }
done
if grep -Eiq 'REPLACE_WITH|smtp\.example\.|@example\.|\.invalid([[:space:]":]|$)' "$config"; then
  echo "Account configuration still contains example placeholders." >&2
  exit 1
fi
if ! grep -Eq '^QSDM_ACCOUNT_DATA_KEY=.+' "$config"; then
  echo "QSDM_ACCOUNT_DATA_KEY is missing from the account configuration." >&2
  exit 1
fi
if ! grep -Eq '^QSDM_ACCOUNT_(SMTP_HOST|TELEGRAM_CLIENT_ID)=.+' "$config"; then
  echo "Configure SMTP or Telegram before installing qsdm-account." >&2
  exit 1
fi
if ! id qsdm >/dev/null 2>&1; then
  echo "The qsdm service user does not exist. Install QSDM Core first." >&2
  exit 1
fi

# Validate the new binary and configuration before replacing a healthy
# installation. systemd reads EnvironmentFile using the same rules as the
# final service, so the installer never sources or prints secret values.
if ! systemd-run --quiet --wait --pipe --collect \
  --property="EnvironmentFile=$config" \
  "$binary" --check-config; then
  echo "Account configuration preflight failed; the installed service was not changed." >&2
  exit 1
fi

install -d -m0755 -o root -g root /opt/qsdm /etc/qsdm
install -m0755 -o root -g root "$binary" /opt/qsdm/qsdm-account
install -m0755 -o root -g root "$verifier" /opt/qsdm/verify-account-service
install -m0600 -o root -g root "$config" /etc/qsdm/account.conf
install -m0644 -o root -g root "$unit" /etc/systemd/system/qsdm-account.service

systemctl daemon-reload
systemctl enable --now qsdm-account.service
sleep 1
/opt/qsdm/verify-account-service --local-only
echo "qsdm-account is installed and healthy."
