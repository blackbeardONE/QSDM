#!/usr/bin/env bash
# Full short test run without CGO; avoids stale CGO_* breaking gcc when paths are wrong.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/source"
export CGO_ENABLED=0
export QSDMPLUS_METRICS_REGISTER_STRICT=1
unset CGO_CFLAGS CGO_LDFLAGS 2>/dev/null || true
go test ./... -short -count=1 -timeout 15m
