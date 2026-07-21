#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo "usage: $0 <stage-dir> <agent-version> [webroot]" >&2
  exit 64
fi

stage_dir="$(cd "$1" && pwd)"
agent_version="$2"
webroot="${3:-/var/www/qsdm}"
downloads="$webroot/downloads"
checksum_file="qsdm-edge-agent-${agent_version}-SHA256SUMS.txt"

if [[ ! "$agent_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid Agent version: $agent_version" >&2
  exit 64
fi

release_files=(
  "qsdm-edge-agent-${agent_version}-windows-x86_64.zip"
  "qsdm-edge-agent-${agent_version}-linux-x86_64.tar.gz"
  "qsdm-edge-agent-${agent_version}-windows-x86_64.exe"
  "qsdm-edge-agent-${agent_version}-linux-x86_64"
  "qsdm-edge-control-${agent_version}-windows-x86_64.exe"
  "qsdm-edge-control-${agent_version}-linux-x86_64"
  "qsdm-edge-gpu-helper-${agent_version}-windows-x86_64.exe"
  "qsdm-edge-gpu-helper-${agent_version}-linux-x86_64"
  "$checksum_file"
)

for file in "${release_files[@]}"; do
  if [[ ! -f "$stage_dir/$file" ]]; then
    echo "missing Edge release file: $stage_dir/$file" >&2
    exit 1
  fi
done

echo "=== verifying Edge release checksums ==="
(
  cd "$stage_dir"
  sha256sum -c "$checksum_file"
)

chmod 0755 \
  "$stage_dir/qsdm-edge-agent-${agent_version}-linux-x86_64" \
  "$stage_dir/qsdm-edge-control-${agent_version}-linux-x86_64" \
  "$stage_dir/qsdm-edge-gpu-helper-${agent_version}-linux-x86_64"

agent_version_output="$($stage_dir/qsdm-edge-agent-${agent_version}-linux-x86_64 version)"
control_version_output="$($stage_dir/qsdm-edge-control-${agent_version}-linux-x86_64 version)"
grep -Fqx "qsdm-edge-agent ${agent_version} (linux/amd64)" <<<"$agent_version_output"
grep -Fqx "qsdm-edge-control ${agent_version} (linux/amd64)" <<<"$control_version_output"

echo "=== verifying Edge release archives ==="
linux_archive_entries="$(
  tar -tzf "$stage_dir/qsdm-edge-agent-${agent_version}-linux-x86_64.tar.gz"
)"
grep -Fq "qsdm-edge-agent-${agent_version}-linux-x86_64/qsdm-edge-agent" \
  <<<"$linux_archive_entries"
grep -Fq "qsdm-edge-agent-${agent_version}-linux-x86_64/qsdm-edge-control" \
  <<<"$linux_archive_entries"
windows_archive="$stage_dir/qsdm-edge-agent-${agent_version}-windows-x86_64.zip"
if command -v unzip >/dev/null 2>&1; then
  unzip -tqq "$windows_archive"
  windows_archive_entries="$(unzip -Z1 "$windows_archive")"
elif command -v python3 >/dev/null 2>&1; then
  windows_archive_entries="$(
    python3 - "$windows_archive" <<'PY'
import sys
import zipfile

with zipfile.ZipFile(sys.argv[1]) as archive:
    damaged = archive.testzip()
    if damaged is not None:
        raise SystemExit(f"damaged ZIP member: {damaged}")
    print("\n".join(archive.namelist()))
PY
  )"
else
  echo "unzip or python3 is required to verify the Windows Edge bundle" >&2
  exit 1
fi
grep -Fqx "qsdm-edge-agent.exe" <<<"$windows_archive_entries"
grep -Fqx "QSDM Edge Control.exe" <<<"$windows_archive_entries"

install -d -o caddy -g caddy -m 0755 "$downloads"

atomic_install_immutable() {
  local source="$1"
  local destination="$2"
  local mode="$3"
  local temporary="${destination}.new.$$"

  if [[ -f "$destination" ]]; then
    if cmp -s "$source" "$destination"; then
      echo "already published: $(basename "$destination")"
      return
    fi
    echo "refusing to replace different immutable release file: $destination" >&2
    exit 1
  fi

  install -o caddy -g caddy -m "$mode" "$source" "$temporary"
  mv -f "$temporary" "$destination"
  echo "published: $(basename "$destination")"
}

echo "=== publishing immutable Edge release files ==="
for file in "${release_files[@]}"; do
  mode=0644
  case "$file" in
    *-linux-x86_64) mode=0755 ;;
  esac
  atomic_install_immutable "$stage_dir/$file" "$downloads/$file" "$mode"
done

echo "=== checking public Edge release URLs ==="
for file in "${release_files[@]}"; do
  curl --fail --silent --show-error --head --max-time 30 \
    "https://qsdm.tech/downloads/$file" >/dev/null
  echo "available: https://qsdm.tech/downloads/$file"
done

echo "Published QSDM Agent and Relay utilities ${agent_version}."
