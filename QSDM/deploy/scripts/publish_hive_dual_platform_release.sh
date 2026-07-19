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

windows_installer="qsdm-hive-${hive_version}-win-x64.exe"
linux_appimage="qsdm-hive-${hive_version}-linux-x86_64.AppImage"
linux_archive="qsdm-hive-${hive_version}-linux-x64.tar.gz"
linux_checksums="qsdm-hive-${hive_version}-linux-SHA256SUMS.txt"
wallet_extension="qsdm-hive-wallet-extension-${wallet_extension_version}.zip"
wallet_extension_checksums="qsdm-hive-wallet-extension-${wallet_extension_version}-SHA256SUMS.txt"

immutable_downloads=(
  "$windows_installer"
  "${windows_installer}.blockmap"
  "qsdm-hive-${hive_version}-release-provenance.json"
  "qsdm-hive-${hive_version}-windows-metadata-evidence.json"
  "qsdm-hive-${hive_version}-windows-nsis-evidence.json"
  "$linux_appimage"
  "$linux_archive"
  "$linux_checksums"
  "qsdm-hive-${hive_version}-linux-release-provenance.json"
  "qsdm-hive-${hive_version}-linux-payload-evidence.json"
  "$wallet_extension"
  "$wallet_extension_checksums"
)
pointer_downloads=(
  "SHA256SUMS-win.txt"
  "latest.yml"
  "latest-linux.yml"
  "qsdm-hive-release-windows.json"
  "qsdm-hive-release-linux.json"
)

for file in "${immutable_downloads[@]}" "${pointer_downloads[@]}"; do
  test -f "$stage_dir/downloads/$file"
done
test -f "$stage_dir/download.html"

(
  cd "$stage_dir/downloads"
  sha256sum -c SHA256SUMS-win.txt
  sha256sum -c "$linux_checksums"
  sha256sum -c "$wallet_extension_checksums"
)
grep -qx "version: ${hive_version}" "$stage_dir/downloads/latest.yml"
grep -qx "version: ${hive_version}" "$stage_dir/downloads/latest-linux.yml"
grep -q "url: ${windows_installer}" "$stage_dir/downloads/latest.yml"
grep -q "url: ${linux_appimage}" "$stage_dir/downloads/latest-linux.yml"
grep -q "$wallet_extension" "$stage_dir/download.html"
grep -q "Version ${hive_version}" "$stage_dir/download.html"

for platform in windows linux; do
  envelope="$stage_dir/downloads/qsdm-hive-release-${platform}.json"
  grep -q '"schema": "qsdm.signed-release.v1"' "$envelope"
  grep -q '"key_id": "10ab9c5710761d4c9dca59d42446e9ea0e3315d15cdc3715df1dcb8c96fa07a1"' \
    "$envelope"
  payload="$(sed -n 's/.*"manifest_base64": "\([^"]*\)".*/\1/p' "$envelope")"
  test -n "$payload"
  printf '%s' "$payload" | base64 --decode | \
    grep -q '"version": "'"${hive_version}"'"'
done

install -d -o caddy -g caddy -m 0755 "$webroot" "$downloads"

atomic_install_immutable() {
  local source="$1"
  local destination="$2"
  local temporary="${destination}.new.$$"

  if [[ -e "$destination" ]]; then
    cmp --silent "$source" "$destination" || {
      echo "refusing to replace immutable release artifact: $destination" >&2
      exit 1
    }
    return
  fi

  install -o caddy -g caddy -m 0644 "$source" "$temporary"
  mv "$temporary" "$destination"
}

install_pointer() {
  local source="$1"
  local destination="$2"
  local temporary="${destination}.new.$$"
  install -o caddy -g caddy -m 0644 "$source" "$temporary"
  mv -f "$temporary" "$destination"
}

# Publish every immutable byte before either platform's exact-version pointer.
for file in "${immutable_downloads[@]}"; do
  atomic_install_immutable "$stage_dir/downloads/$file" "$downloads/$file"
done

for file in "$windows_installer" "$linux_appimage" "$linux_archive" \
  "$wallet_extension" "$wallet_extension_checksums"; do
  curl --fail --silent --show-error --head --max-time 30 \
    "https://qsdm.tech/downloads/$file" >/dev/null
done

install_pointer "$stage_dir/download.html" "$webroot/download.html"
for file in "${pointer_downloads[@]}"; do
  install_pointer "$stage_dir/downloads/$file" "$downloads/$file"
done

curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/downloads/latest.yml" | grep -qx "version: ${hive_version}"
curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/downloads/latest-linux.yml" | grep -qx "version: ${hive_version}"
for platform in windows linux; do
  curl --fail --silent --show-error --max-time 30 \
    "https://qsdm.tech/downloads/qsdm-hive-release-${platform}.json" | \
    grep -q '"schema": "qsdm.signed-release.v1"'
done
curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/download.html" | grep -q "Version ${hive_version}"

echo "Published QSDM Hive ${hive_version} for Windows and Linux atomically."
