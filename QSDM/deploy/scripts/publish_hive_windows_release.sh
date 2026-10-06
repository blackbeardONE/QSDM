#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo "usage: $0 <stage-dir> <hive-version> [webroot]" >&2
  exit 64
fi

stage_dir="$(cd "$1" && pwd)"
hive_version="$2"
webroot="${3:-/var/www/qsdm}"
# Hive 1.4.21+ trusts only the v2 release key and reads its release from the
# self-contained hive-v2 channel. The v1 files directly under /downloads are
# left untouched for Hive <= 1.4.20.
release_channel="hive-v2"
release_key_id="4081bf2c4755f4c5c1565b4fac75e14a7e0b52042ac3d34bf8a7f525866c64a9"
downloads="$webroot/downloads/$release_channel"
public_downloads="https://qsdm.tech/downloads/$release_channel"

# Signed envelopes and manifests are written by PowerShell ConvertTo-Json,
# which may put more than one space after a colon. Match a JSON string field
# exactly, whatever the whitespace. Reads the JSON text from stdin.
json_field_is() {
  local field="$1"
  local escaped
  escaped="$(printf '%s' "$2" | sed 's/[][\.*^$+?(){}|]/\\&/g')"
  grep -Eq "\"${field}\":[[:space:]]*\"${escaped}\""
}
wallet_extension_version="${QSDM_HIVE_WALLET_EXTENSION_VERSION:-0.5.1}"

if [[ ! "$hive_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid Hive version: $hive_version" >&2
  exit 64
fi

installer="qsdm-hive-${hive_version}-win-x64.exe"
blockmap="${installer}.blockmap"
wallet_extension="qsdm-hive-wallet-extension-${wallet_extension_version}.zip"
wallet_extension_crx="qsdm-hive-wallet-extension-${wallet_extension_version}.crx"
wallet_extension_chromium="qsdm-hive-wallet-extension-${wallet_extension_version}-chromium.zip"
wallet_extension_chrome="qsdm-hive-wallet-extension-${wallet_extension_version}-chrome.zip"
wallet_extension_edge="qsdm-hive-wallet-extension-${wallet_extension_version}-edge.zip"
wallet_extension_brave="qsdm-hive-wallet-extension-${wallet_extension_version}-brave.zip"
wallet_extension_firefox="qsdm-hive-wallet-extension-${wallet_extension_version}-firefox.zip"
wallet_extension_checksums="qsdm-hive-wallet-extension-${wallet_extension_version}-SHA256SUMS.txt"
required_downloads=(
  "$installer"
  "$blockmap"
  "SHA256SUMS-win.txt"
  "latest.yml"
  "qsdm-hive-${hive_version}-release-provenance.json"
  "qsdm-hive-${hive_version}-windows-metadata-evidence.json"
  "qsdm-hive-${hive_version}-windows-nsis-evidence.json"
  "$wallet_extension"
  "$wallet_extension_chromium"
  "$wallet_extension_chrome"
  "$wallet_extension_edge"
  "$wallet_extension_brave"
  "$wallet_extension_firefox"
  "$wallet_extension_checksums"
  "qsdm-hive-release-windows-v2.json"
)
if [[ -f "$stage_dir/downloads/$wallet_extension_crx" ]]; then
  required_downloads+=("$wallet_extension_crx")
fi

for file in "${required_downloads[@]}"; do
  test -f "$stage_dir/downloads/$file"
done
test -f "$stage_dir/download.html"

(
  cd "$stage_dir/downloads"
  sha256sum -c SHA256SUMS-win.txt
  sha256sum -c "$wallet_extension_checksums"
)
grep -qx "version: ${hive_version}" "$stage_dir/downloads/latest.yml"
grep -q "url: ${installer}" "$stage_dir/downloads/latest.yml"
grep -Eq '"schema":[[:space:]]*"qsdm\.signed-release\.v1"' \
  "$stage_dir/downloads/qsdm-hive-release-windows-v2.json"
json_field_is key_id "$release_key_id" \
  <"$stage_dir/downloads/qsdm-hive-release-windows-v2.json"
manifest_payload="$(sed -n 's/.*"manifest_base64":[[:space:]]*"\([^"]*\)".*/\1/p' \
  "$stage_dir/downloads/qsdm-hive-release-windows-v2.json")"
test -n "$manifest_payload"
manifest_json="$(printf '%s' "$manifest_payload" | base64 --decode)"
json_field_is version "${hive_version}" <<<"$manifest_json"
json_field_is name "${wallet_extension}" <<<"$manifest_json"
if [[ -f "$stage_dir/downloads/$wallet_extension_crx" ]]; then
  json_field_is name "${wallet_extension_crx}" <<<"$manifest_json"
fi
json_field_is name "${wallet_extension_chromium}" <<<"$manifest_json"
json_field_is name "${wallet_extension_chrome}" <<<"$manifest_json"
json_field_is name "${wallet_extension_edge}" <<<"$manifest_json"
json_field_is name "${wallet_extension_brave}" <<<"$manifest_json"
json_field_is name "${wallet_extension_firefox}" <<<"$manifest_json"

install -d -o caddy -g caddy -m 0755 "$webroot" "$webroot/downloads" "$downloads"

atomic_install() {
  local source="$1"
  local destination="$2"
  local mode="${3:-0644}"
  local temporary="${destination}.new.$$"

  if [[ -e "$destination" ]]; then
    cmp --silent "$source" "$destination" || {
      echo "refusing to replace immutable release artifact: $destination" >&2
      exit 1
    }
    return
  fi

  install -o caddy -g caddy -m "$mode" "$source" "$temporary"
  mv "$temporary" "$destination"
}

# Immutable payloads become public before the page or updater manifest.
for file in \
  "$installer" \
  "$blockmap" \
  "qsdm-hive-${hive_version}-release-provenance.json" \
  "qsdm-hive-${hive_version}-windows-metadata-evidence.json" \
  "qsdm-hive-${hive_version}-windows-nsis-evidence.json" \
  "$wallet_extension" \
  "$wallet_extension_chromium" \
  "$wallet_extension_chrome" \
  "$wallet_extension_edge" \
  "$wallet_extension_brave" \
  "$wallet_extension_firefox" \
  "$wallet_extension_checksums"; do
  atomic_install "$stage_dir/downloads/$file" "$downloads/$file"
done
if [[ -f "$stage_dir/downloads/$wallet_extension_crx" ]]; then
  atomic_install "$stage_dir/downloads/$wallet_extension_crx" \
    "$downloads/$wallet_extension_crx"
fi

install_pointer() {
  local source="$1"
  local destination="$2"
  local temporary="${destination}.new.$$"
  install -o caddy -g caddy -m 0644 "$source" "$temporary"
  mv -f "$temporary" "$destination"
}

install_pointer "$stage_dir/downloads/SHA256SUMS-win.txt" "$downloads/SHA256SUMS-win.txt"

for file in "$installer" "$wallet_extension" \
  "$wallet_extension_chromium" \
  "$wallet_extension_chrome" "$wallet_extension_edge" \
  "$wallet_extension_brave" \
  "$wallet_extension_firefox" "$wallet_extension_checksums"; do
  curl --fail --silent --show-error --head --max-time 30 \
    "$public_downloads/$file" >/dev/null
done
if [[ -f "$stage_dir/downloads/$wallet_extension_crx" ]]; then
  curl --fail --silent --show-error --head --max-time 30 \
    "$public_downloads/$wallet_extension_crx" >/dev/null
fi

install_pointer "$stage_dir/download.html" "$webroot/download.html"

# Exact-version clients see the release only after every referenced byte is public.
install_pointer "$stage_dir/downloads/latest.yml" "$downloads/latest.yml"
install_pointer "$stage_dir/downloads/qsdm-hive-release-windows-v2.json" \
  "$downloads/qsdm-hive-release-windows-v2.json"

public_latest="$(curl --fail --silent --show-error --max-time 30 \
  "$public_downloads/latest.yml")"
grep -qx "version: ${hive_version}" <<<"$public_latest"
public_envelope="$(curl --fail --silent --show-error --max-time 30 \
  "$public_downloads/qsdm-hive-release-windows-v2.json")"
grep -Eq '"schema":[[:space:]]*"qsdm\.signed-release\.v1"' <<<"$public_envelope"
public_download_page="$(curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/download.html")"
grep -q "Version ${hive_version}" <<<"$public_download_page"

echo "Published QSDM Hive ${hive_version} for Windows. Linux manifests unchanged."
