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

appimage="qsdm-hive-${hive_version}-linux-x86_64.AppImage"
archive="qsdm-hive-${hive_version}-linux-x64.tar.gz"
checksums="qsdm-hive-${hive_version}-linux-SHA256SUMS.txt"
provenance="qsdm-hive-${hive_version}-linux-release-provenance.json"
evidence="qsdm-hive-${hive_version}-linux-payload-evidence.json"
required_downloads=(
  "$appimage"
  "$archive"
  "$checksums"
  "$provenance"
  "$evidence"
  "latest-linux.yml"
)

for file in "${required_downloads[@]}"; do
  test -f "$stage_dir/downloads/$file"
done
test -f "$stage_dir/download.html"

(
  cd "$stage_dir/downloads"
  sha256sum -c "$checksums"
)
grep -qx "version: ${hive_version}" "$stage_dir/downloads/latest-linux.yml"
grep -q "url: ${appimage}" "$stage_dir/downloads/latest-linux.yml"

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

install_pointer() {
  local source="$1"
  local destination="$2"
  local temporary="${destination}.new.$$"
  install -o caddy -g caddy -m 0644 "$source" "$temporary"
  mv -f "$temporary" "$destination"
}

# Immutable packages and their evidence become public before any pointer moves.
for file in "$appimage" "$archive" "$provenance" "$evidence"; do
  atomic_install "$stage_dir/downloads/$file" "$downloads/$file"
done
install_pointer "$stage_dir/downloads/$checksums" "$downloads/$checksums"

for file in "$appimage" "$archive" "$checksums"; do
  curl --fail --silent --show-error --head --max-time 30 \
    "https://qsdm.tech/downloads/$file" >/dev/null
done

install_pointer "$stage_dir/download.html" "$webroot/download.html"

# The Linux exact-version policy moves last. Windows latest.yml is untouched.
install_pointer "$stage_dir/downloads/latest-linux.yml" "$downloads/latest-linux.yml"

curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/downloads/latest-linux.yml" | grep -qx "version: ${hive_version}"
curl --fail --silent --show-error --max-time 30 \
  "https://qsdm.tech/download.html" | grep -q "qsdm-hive-${hive_version}-linux-x86_64.AppImage"

echo "Published QSDM Hive ${hive_version} for Linux. Windows manifest unchanged."
