#!/usr/bin/env bash
set -euo pipefail

extension_id="${1:-habkkkednignfkoffhpbjahcjbikkahh}"
firefox_extension_id="${3:-qsdm-wallet@qsdm.tech}"
if [[ ! "$extension_id" =~ ^[a-p]{32}$ ]]; then
  echo "usage: $0 [32-character-extension-id] [native-host-path] [firefox-extension-id]" >&2
  exit 64
fi
if [[ ! "$firefox_extension_id" =~ ^[A-Za-z0-9._+@-]{1,128}$ ]]; then
  echo "Firefox extension ID contains unsupported characters." >&2
  exit 64
fi

host_path="${2:-$(cd "$(dirname "$0")/../../native" && pwd)/qsdm-hive-wallet-host}"
host_path="$(readlink -f "$host_path")"
if [[ ! -x "$host_path" ]]; then
  echo "QSDM native messaging host is missing or not executable: $host_path" >&2
  exit 66
fi
if [[ "$host_path" == *\"* || "$host_path" == *$'\n'* || "$host_path" == *$'\r'* ]]; then
  echo "QSDM native messaging host path contains unsupported characters." >&2
  exit 64
fi

chromium_manifest="$(cat <<JSON
{
  "name": "tech.qsdm.hive_wallet",
  "description": "QSDM Hive Wallet native bridge",
  "path": "$host_path",
  "type": "stdio",
  "allowed_origins": ["chrome-extension://$extension_id/"]
}
JSON
)"

firefox_manifest="$(cat <<JSON
{
  "name": "tech.qsdm.hive_wallet",
  "description": "QSDM Hive Wallet native bridge",
  "path": "$host_path",
  "type": "stdio",
  "allowed_extensions": ["$firefox_extension_id"]
}
JSON
)"

for directory in \
  "$HOME/.config/google-chrome/NativeMessagingHosts" \
  "$HOME/.config/chromium/NativeMessagingHosts" \
  "$HOME/.config/microsoft-edge/NativeMessagingHosts" \
  "$HOME/.config/BraveSoftware/Brave-Browser/NativeMessagingHosts"; do
  mkdir -p "$directory"
  printf '%s\n' "$chromium_manifest" > "$directory/tech.qsdm.hive_wallet.json"
  chmod 0600 "$directory/tech.qsdm.hive_wallet.json"
done

firefox_directory="$HOME/.mozilla/native-messaging-hosts"
mkdir -p "$firefox_directory"
printf '%s\n' "$firefox_manifest" > "$firefox_directory/tech.qsdm.hive_wallet.json"
chmod 0600 "$firefox_directory/tech.qsdm.hive_wallet.json"

echo "QSDM Wallet bridge registered for Chromium $extension_id and Firefox $firefox_extension_id"
