#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: sudo activate_account_service_interactive.sh BINARY [CLIENT_ID]

Prompts for the Telegram OpenID Connect client secret without echoing it,
creates a new QSDM Account encryption key, installs the private service, and
activates the public route only after local verification succeeds.

This first-install helper refuses to replace an existing account.conf so it
cannot accidentally rotate the key used to encrypt an account store.
USAGE
}

if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  echo "Run this activation helper as root." >&2
  exit 1
fi
if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage >&2
  exit 2
fi
if [[ ! -r /dev/tty || ! -w /dev/tty ]]; then
  echo "A real interactive terminal is required for secret entry." >&2
  exit 1
fi
if [[ -e /etc/qsdm/account.conf ]]; then
  echo "/etc/qsdm/account.conf already exists; refusing first-install activation." >&2
  echo "Use the normal account-service update procedure to preserve its data key." >&2
  exit 1
fi

binary=$(realpath "$1")
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
installer=$script_dir/install_account_service.sh

for path in "$binary" "$installer"; do
  if [[ ! -f "$path" ]]; then
    echo "Required file is missing: $path" >&2
    exit 1
  fi
done
for command_name in openssl realpath; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "Required command is missing: $command_name" >&2
    exit 1
  }
done

client_id=${2:-}
if [[ -z $client_id ]]; then
  printf 'Telegram OpenID Connect Client ID: ' >/dev/tty
  IFS= read -r client_id </dev/tty
fi
if [[ ! $client_id =~ ^[0-9]{5,20}$ ]]; then
  echo "Telegram Client ID must contain only 5 to 20 digits." >&2
  exit 1
fi

printf 'Telegram OpenID Connect Client Secret: ' >/dev/tty
IFS= read -r -s telegram_secret </dev/tty
printf '\n' >/dev/tty
if [[ ! $telegram_secret =~ ^[A-Za-z0-9._~+/=-]{20,512}$ ]]; then
  unset telegram_secret
  echo "Telegram Client Secret has an unexpected format." >&2
  exit 1
fi

umask 077
config=$(mktemp /tmp/qsdm-account.conf.XXXXXXXX)
cleanup() {
  unset telegram_secret
  rm -f -- "$config"
}
trap cleanup EXIT

data_key=$(openssl rand -base64 32 | tr -d '\r\n')
cat >"$config" <<EOF
QSDM_ACCOUNT_LISTEN=127.0.0.1:8092
QSDM_ACCOUNT_PUBLIC_BASE_URL=https://qsdm.tech
QSDM_ACCOUNT_STORE_PATH=/var/lib/qsdm-account/accounts.json
QSDM_ACCOUNT_DATA_KEY=$data_key
QSDM_ACCOUNT_TELEGRAM_CLIENT_ID=$client_id
QSDM_ACCOUNT_TELEGRAM_CLIENT_SECRET=$telegram_secret
EOF
chmod 0600 "$config"

"$installer" "$binary" "$config"
/opt/qsdm/install-account-proxy-route
/opt/qsdm/verify-account-service --check-telegram

echo "QSDM Account is active and Telegram OpenID Connect routing is healthy."
echo "Complete one real Telegram sign-in at https://qsdm.tech/account/."
