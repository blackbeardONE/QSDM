#!/usr/bin/env bash
# Build cmd/qsdm with CGO off; clears stale CGO_* (same idea as go-test-short-no-cgo.sh).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/source"
export CGO_ENABLED=0
unset CGO_CFLAGS CGO_LDFLAGS 2>/dev/null || true
OUT="${1:-qsdm}"
go build -o "$OUT" ./cmd/qsdm
