#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo "usage: $0 <stage-dir> <preview-version> [webroot]" >&2
  exit 64
fi

stage_dir="$(cd "$1" && pwd)"
preview_version="$2"
webroot="${3:-/var/www/qsdm}"
downloads="$webroot/downloads"
preview_downloads="$downloads/unsigned-preview"
stage_preview="$stage_dir/downloads/unsigned-preview"

if [[ ! "$preview_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+-unsigned-preview\.[0-9]+$ ]]; then
  echo "invalid unsigned preview version: $preview_version" >&2
  exit 64
fi

installer="qsdm-hive-${preview_version}-win-x64.exe"
immutable_files=(
  "$installer"
  "${installer}.blockmap"
  "qsdm-hive-${preview_version}-SHA256SUMS.txt"
  "qsdm-hive-${preview_version}-windows-metadata-evidence.json"
  "qsdm-hive-${preview_version}-windows-nsis-evidence.json"
  "qsdm-hive-${preview_version}-release-provenance.json"
)

for file in "${immutable_files[@]}" latest.yml; do
  test -f "$stage_preview/$file"
done
test -f "$stage_dir/download.html"

(
  cd "$stage_preview"
  sha256sum -c "qsdm-hive-${preview_version}-SHA256SUMS.txt"
)
grep -qx "version: ${preview_version}" "$stage_preview/latest.yml"
grep -q "path: ${installer}" "$stage_preview/latest.yml"
grep -q "$preview_version" "$stage_dir/download.html"

stable_manifest="$downloads/latest.yml"
test -f "$stable_manifest"
stable_hash_before="$(sha256sum "$stable_manifest" | cut -d' ' -f1)"

install -d -o caddy -g caddy -m 0755 "$webroot" "$downloads" "$preview_downloads"

install_immutable() {
  local source="$1"
  local destination="$2"
  if [[ -e "$destination" ]]; then
    cmp -s "$source" "$destination" || {
      echo "refusing to replace immutable preview artifact: $destination" >&2
      exit 73
    }
    return
  fi
  install -o caddy -g caddy -m 0644 "$source" "$destination"
}

atomic_install() {
  local source="$1"
  local destination="$2"
  local temporary="${destination}.new.$$"
  install -o caddy -g caddy -m 0644 "$source" "$temporary"
  mv -f "$temporary" "$destination"
}

for file in "${immutable_files[@]}"; do
  install_immutable "$stage_preview/$file" "$preview_downloads/$file"
done

curl --fail --silent --show-error --head --max-time 30 \
  "https://qsdm.tech/downloads/unsigned-preview/$installer" >/dev/null

atomic_install "$stage_dir/download.html" "$webroot/download.html"
atomic_install "$stage_preview/latest.yml" "$preview_downloads/latest.yml"

curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/downloads/unsigned-preview/latest.yml" | \
  grep -qx "version: ${preview_version}"

stable_hash_after="$(sha256sum "$stable_manifest" | cut -d' ' -f1)"
if [[ "$stable_hash_before" != "$stable_hash_after" ]]; then
  echo "stable updater manifest changed during preview publication" >&2
  exit 70
fi

echo "Published isolated QSDM Hive ${preview_version}. Stable updater unchanged."
