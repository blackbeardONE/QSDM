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

if [[ ! "$hive_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid Hive version: $hive_version" >&2
  exit 64
fi

installer="qsdm-hive-${hive_version}-win-x64.exe"
blockmap="${installer}.blockmap"
required_downloads=(
  "$installer"
  "$blockmap"
  "SHA256SUMS-win.txt"
  "latest.yml"
  "qsdm-hive-${hive_version}-release-provenance.json"
  "qsdm-hive-${hive_version}-windows-metadata-evidence.json"
  "qsdm-hive-${hive_version}-windows-nsis-evidence.json"
)

for file in "${required_downloads[@]}"; do
  test -f "$stage_dir/downloads/$file"
done
test -f "$stage_dir/download.html"

(
  cd "$stage_dir/downloads"
  sha256sum -c SHA256SUMS-win.txt
)
grep -qx "version: ${hive_version}" "$stage_dir/downloads/latest.yml"
grep -q "url: ${installer}" "$stage_dir/downloads/latest.yml"

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
  "qsdm-hive-${hive_version}-windows-nsis-evidence.json"; do
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

curl --fail --silent --show-error --head --max-time 30 \
  "https://qsdm.tech/downloads/$installer" >/dev/null

install_pointer "$stage_dir/download.html" "$webroot/download.html"

# Exact-version clients see the release only after every referenced byte is public.
install_pointer "$stage_dir/downloads/latest.yml" "$downloads/latest.yml"

curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/downloads/latest.yml" | grep -qx "version: ${hive_version}"
curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/download.html" | grep -q "Version ${hive_version}"

echo "Published QSDM Hive ${hive_version} for Windows. Linux manifests unchanged."
