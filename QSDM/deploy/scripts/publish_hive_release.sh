#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 || $# -gt 4 ]]; then
  echo "usage: $0 <stage-dir> <hive-version> <agent-version> [webroot]" >&2
  exit 64
fi

stage_dir="$(cd "$1" && pwd)"
hive_version="$2"
agent_version="$3"
webroot="${4:-/var/www/qsdm}"
downloads="$webroot/downloads"
wallet_extension_version="${QSDM_HIVE_WALLET_EXTENSION_VERSION:-0.2.0}"
wallet_extension="qsdm-hive-wallet-extension-${wallet_extension_version}.zip"
wallet_extension_checksums="qsdm-hive-wallet-extension-${wallet_extension_version}-SHA256SUMS.txt"

if [[ ! "$hive_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid Hive version: $hive_version" >&2
  exit 64
fi
if [[ ! "$agent_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid Agent version: $agent_version" >&2
  exit 64
fi

immutable_downloads=(
  "qsdm-hive-${hive_version}-win-x64.exe"
  "qsdm-hive-${hive_version}-win-x64.exe.blockmap"
  "qsdm-hive-${hive_version}-linux-x86_64.AppImage"
  "qsdm-hive-${hive_version}-linux-x64.tar.gz"
  "SHA256SUMS-win.txt"
  "qsdm-hive-${hive_version}-linux-SHA256SUMS.txt"
  "$wallet_extension"
  "$wallet_extension_checksums"
  "qsdm-edge-agent-${agent_version}-windows-x86_64.zip"
  "qsdm-edge-agent-${agent_version}-linux-x86_64.tar.gz"
  "qsdm-edge-agent-${agent_version}-windows-x86_64.exe"
  "qsdm-edge-agent-${agent_version}-linux-x86_64"
  "qsdm-edge-control-${agent_version}-windows-x86_64.exe"
  "qsdm-edge-control-${agent_version}-linux-x86_64"
  "qsdm-edge-gpu-helper-${agent_version}-windows-x86_64.exe"
  "qsdm-edge-gpu-helper-${agent_version}-linux-x86_64"
  "qsdm-edge-agent-${agent_version}-SHA256SUMS.txt"
)
update_manifests=(
  latest.yml
  alpha.yml
  beta.yml
  latest-linux.yml
  alpha-linux.yml
  beta-linux.yml
)
signed_release_manifests=(
  qsdm-hive-release-windows.json
  qsdm-hive-release-linux.json
)

for file in "${immutable_downloads[@]}" "${update_manifests[@]}" \
  "${signed_release_manifests[@]}"; do
  test -f "$stage_dir/downloads/$file"
done
for file in index.html download.html docs/index.html docs/docs.js; do
  test -f "$stage_dir/$file"
done

(
  cd "$stage_dir/downloads"
  sha256sum -c SHA256SUMS-win.txt
  sha256sum -c "qsdm-hive-${hive_version}-linux-SHA256SUMS.txt"
  sha256sum -c "$wallet_extension_checksums"
  sha256sum -c "qsdm-edge-agent-${agent_version}-SHA256SUMS.txt"
)

for manifest in "${update_manifests[@]}"; do
  grep -qx "version: ${hive_version}" "$stage_dir/downloads/$manifest"
done
windows_payload="$(sed -n 's/.*"manifest_base64": "\([^"]*\)".*/\1/p' \
  "$stage_dir/downloads/qsdm-hive-release-windows.json")"
test -n "$windows_payload"
printf '%s' "$windows_payload" | base64 --decode | \
  grep -q '"name": "'"${wallet_extension}"'"'
for manifest in "${signed_release_manifests[@]}"; do
  grep -q '"schema": "qsdm.signed-release.v1"' "$stage_dir/downloads/$manifest"
  grep -q '"key_id": "10ab9c5710761d4c9dca59d42446e9ea0e3315d15cdc3715df1dcb8c96fa07a1"' \
    "$stage_dir/downloads/$manifest"
  manifest_payload="$(sed -n 's/.*"manifest_base64": "\([^"]*\)".*/\1/p' \
    "$stage_dir/downloads/$manifest")"
  test -n "$manifest_payload"
  printf '%s' "$manifest_payload" | base64 --decode | \
    grep -q '"version": "'"${hive_version}"'"'
done

install -d -o caddy -g caddy -m 0755 "$webroot" "$downloads" "$webroot/docs"

atomic_install() {
  local source="$1"
  local destination="$2"
  local mode="${3:-0644}"
  local temporary="${destination}.new.$$"
  install -o caddy -g caddy -m "$mode" "$source" "$temporary"
  mv -f "$temporary" "$destination"
}

# Packages go live before clients or pages are told that the release exists.
for file in "${immutable_downloads[@]}"; do
  atomic_install "$stage_dir/downloads/$file" "$downloads/$file"
done

for file in \
  "qsdm-hive-${hive_version}-win-x64.exe" \
  "qsdm-hive-${hive_version}-linux-x86_64.AppImage" \
  "qsdm-hive-${hive_version}-linux-x64.tar.gz" \
  "$wallet_extension" \
  "$wallet_extension_checksums" \
  "qsdm-edge-agent-${agent_version}-windows-x86_64.zip" \
  "qsdm-edge-agent-${agent_version}-linux-x86_64.tar.gz"; do
  curl --fail --silent --show-error --head --max-time 30 \
    "https://qsdm.tech/downloads/$file" >/dev/null
done

atomic_install "$stage_dir/index.html" "$webroot/index.html"
atomic_install "$stage_dir/download.html" "$webroot/download.html"
atomic_install "$stage_dir/docs/index.html" "$webroot/docs/index.html"
atomic_install "$stage_dir/docs/docs.js" "$webroot/docs/docs.js"

# Exact-version clients see the new release only after every package is public.
for file in "${update_manifests[@]}"; do
  atomic_install "$stage_dir/downloads/$file" "$downloads/$file"
done
for file in "${signed_release_manifests[@]}"; do
  atomic_install "$stage_dir/downloads/$file" "$downloads/$file"
done

curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/downloads/latest.yml" | grep -qx "version: ${hive_version}"
for manifest in "${signed_release_manifests[@]}"; do
  curl --fail --silent --show-error --max-time 30 \
    "https://qsdm.tech/downloads/$manifest" | \
    grep -q '"schema": "qsdm.signed-release.v1"'
done
curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/download.html" | grep -q "Agent and Relay utilities"

echo "Published QSDM Hive ${hive_version} with Agent ${agent_version}."
