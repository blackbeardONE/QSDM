#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || ! "$1" =~ ^[a-p]{32}$ ]]; then
  echo "usage: $0 <32-character-extension-id> [native-host-path]" >&2
  exit 64
fi

extension_id="$1"
host_path="${2:-$(cd "$(dirname "$0")/../../native" && pwd)/qsdm-hive-wallet-host}"
host_path="$(readlink -f "$host_path")"
if [[ ! -x "$host_path" ]]; then
  echo "QSDM native messaging host is missing or not executable: $host_path" >&2
  exit 66
fi

manifest="$(cat <<JSON
{
  "name": "tech.qsdm.hive_wallet",
  "description": "QSDM Hive Wallet native bridge",
  "path": "$host_path",
  "type": "stdio",
  "allowed_origins": ["chrome-extension://$extension_id/"]
}
JSON
)"

for directory in \
  "$HOME/.config/google-chrome/NativeMessagingHosts" \
  "$HOME/.config/chromium/NativeMessagingHosts" \
  "$HOME/.config/microsoft-edge/NativeMessagingHosts"; do
  mkdir -p "$directory"
  printf '%s\n' "$manifest" > "$directory/tech.qsdm.hive_wallet.json"
  chmod 0600 "$directory/tech.qsdm.hive_wallet.json"
done

echo "QSDM Hive Wallet bridge registered for extension $extension_id"
