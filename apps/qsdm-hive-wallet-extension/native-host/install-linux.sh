#!/usr/bin/env bash
set -euo pipefail

# Every extension Hive trusts must be listed, because Chrome refuses a native
# messaging connection from any extension absent from allowed_origins. This
# must stay in step with QSDM_WALLET_TRUSTED_EXTENSION_IDS in
# apps/qsdm-hive/qsdm-hive-main/src/main/services/qsdmWalletProviderNativeHost.ts;
# a spec in that file asserts this script lists all of them.
#
# Writing a single ID was a real defect: a host registered by this script
# accepted only the manually loaded Chromium build, so a wallet installed from
# the Chrome Web Store was refused even though Hive itself trusted it.
qsdm_trusted_extension_ids=(
  habkkkednignfkoffhpbjahcjbikkahh # manually loaded Chromium build (pinned key)
  homapjeinjlbdjhhdegcbnldkpkodepo # Chrome Web Store listing
  nmmhneekhgaegpmbnhiacglhoncicflc # interim CRX
)

# An explicitly supplied ID is ADDED to the trusted set rather than replacing
# it, so registering a development build cannot silently disconnect the
# shipped ones.
if [[ -n "${1:-}" ]]; then
  qsdm_trusted_extension_ids+=("$1")
fi

firefox_extension_id="${3:-qsdm-wallet@qsdm.tech}"

for extension_id in "${qsdm_trusted_extension_ids[@]}"; do
  if [[ ! "$extension_id" =~ ^[a-p]{32}$ ]]; then
    echo "usage: $0 [32-character-extension-id] [native-host-path] [firefox-extension-id]" >&2
    exit 64
  fi
done
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

allowed_origins=""
seen_extension_ids=""
for extension_id in "${qsdm_trusted_extension_ids[@]}"; do
  case " $seen_extension_ids " in
    *" $extension_id "*) continue ;;
  esac
  seen_extension_ids="$seen_extension_ids $extension_id"
  if [[ -n "$allowed_origins" ]]; then
    allowed_origins="$allowed_origins, "
  fi
  allowed_origins="$allowed_origins\"chrome-extension://$extension_id/\""
done

chromium_manifest="$(cat <<JSON
{
  "name": "tech.qsdm.hive_wallet",
  "description": "QSDM Hive Wallet native bridge",
  "path": "$host_path",
  "type": "stdio",
  "allowed_origins": [$allowed_origins]
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

echo "QSDM Wallet bridge registered for Chromium${seen_extension_ids} and Firefox $firefox_extension_id"
