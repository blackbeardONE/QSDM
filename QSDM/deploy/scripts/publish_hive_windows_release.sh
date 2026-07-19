#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo "usage: $0 <stage-dir> <hive-version> [webroot]" >&2
  exit 64
fi

stage_dir="$(cd "$1" && pwd)"
hive_version="$2"
webroot="${3:-/var/www/qsdm}"
downloads="$webroot/downloads"
wallet_extension_version="${QSDM_HIVE_WALLET_EXTENSION_VERSION:-0.2.0}"

if [[ ! "$hive_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid Hive version: $hive_version" >&2
  exit 64
fi

installer="qsdm-hive-${hive_version}-win-x64.exe"
blockmap="${installer}.blockmap"
wallet_extension="qsdm-hive-wallet-extension-${wallet_extension_version}.zip"
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
  "$wallet_extension_checksums"
  "qsdm-hive-release-windows.json"
)

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
grep -q '"schema": "qsdm.signed-release.v1"' \
  "$stage_dir/downloads/qsdm-hive-release-windows.json"
grep -q '"key_id": "10ab9c5710761d4c9dca59d42446e9ea0e3315d15cdc3715df1dcb8c96fa07a1"' \
  "$stage_dir/downloads/qsdm-hive-release-windows.json"
manifest_payload="$(sed -n 's/.*"manifest_base64": "\([^"]*\)".*/\1/p' \
  "$stage_dir/downloads/qsdm-hive-release-windows.json")"
test -n "$manifest_payload"
printf '%s' "$manifest_payload" | base64 --decode | \
  grep -q '"version": "'"${hive_version}"'"'
printf '%s' "$manifest_payload" | base64 --decode | \
  grep -q '"name": "'"${wallet_extension}"'"'

install -d -o caddy -g caddy -m 0755 "$webroot" "$downloads"

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
  "$wallet_extension_checksums"; do
  atomic_install "$stage_dir/downloads/$file" "$downloads/$file"
done

install_pointer() {
  local source="$1"
  local destination="$2"
  local temporary="${destination}.new.$$"
  install -o caddy -g caddy -m 0644 "$source" "$temporary"
  mv -f "$temporary" "$destination"
}

install_pointer "$stage_dir/downloads/SHA256SUMS-win.txt" "$downloads/SHA256SUMS-win.txt"

for file in "$installer" "$wallet_extension" "$wallet_extension_checksums"; do
  curl --fail --silent --show-error --head --max-time 30 \
    "https://qsdm.tech/downloads/$file" >/dev/null
done

install_pointer "$stage_dir/download.html" "$webroot/download.html"

# Exact-version clients see the release only after every referenced byte is public.
install_pointer "$stage_dir/downloads/latest.yml" "$downloads/latest.yml"
install_pointer "$stage_dir/downloads/qsdm-hive-release-windows.json" \
  "$downloads/qsdm-hive-release-windows.json"

curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/downloads/latest.yml" | grep -qx "version: ${hive_version}"
curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/downloads/qsdm-hive-release-windows.json" | \
  grep -q '"schema": "qsdm.signed-release.v1"'
curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/download.html" | grep -q "Version ${hive_version}"

echo "Published QSDM Hive ${hive_version} for Windows. Linux manifests unchanged."
