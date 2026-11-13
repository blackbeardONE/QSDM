#!/usr/bin/env bash
# Builds the native tool QSDM Hive needs on macOS: qsdmcli (wallet and task-action signing, and the check of the signed
# release that the version policy reads), for Intel (x64) and Apple silicon (arm64). It is pure Go (CGO off, the CIRCL
# ML-DSA code), like the Linux build. electron-builder packs native/darwin/<arch>/qsdmcli into each architecture's app
# and merges the two into the universal app.
#
# Nothing else is built for macOS on purpose: the miner, its CUDA solver, the edge helpers and the browser-extension
# native host are Windows and Linux only (mining needs an NVIDIA GPU).
#
# usage: build_hive_macos_native.sh <hive-source-dir>
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <hive-source-dir>" >&2
  exit 64
fi

source_dir="$(cd "$1" && pwd)"
qsdm_source_dir="${QSDM_SOURCE_DIR:-$source_dir/../../../QSDM/source}"

if ! command -v go >/dev/null 2>&1; then
  echo "Go is required to build qsdmcli for macOS." >&2
  exit 69
fi
qsdm_source_dir="$(cd "$qsdm_source_dir" && pwd)"

for arch in x64 arm64; do
  goarch="$arch"
  [[ "$arch" == x64 ]] && goarch=amd64
  out_dir="$source_dir/native/darwin/$arch"
  mkdir -p "$out_dir"
  (
    cd "$qsdm_source_dir"
    CGO_ENABLED=0 GOOS=darwin GOARCH="$goarch" go build \
      -trimpath \
      -tags dilithium_circl \
      -ldflags="-s -w" \
      -o "$out_dir/qsdmcli" \
      ./cmd/qsdmcli
  )
  chmod 0755 "$out_dir/qsdmcli"
  echo "built $out_dir/qsdmcli (darwin/$goarch)"
done
