#!/usr/bin/env bash
# go test -race on hot packages. The race detector requires CGO + a C toolchain (gcc/clang).
# Slower than default CI; use locally or workflow_dispatch. Run from monorepo root.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/source"
export CGO_ENABLED=1
unset CGO_CFLAGS CGO_LDFLAGS 2>/dev/null || true
export QSDMPLUS_METRICS_REGISTER_STRICT=1

echo "==> go test -race -short (mempool, networking, alerting, contracts, state, reputation)"
go test -race -short -count=1 -timeout 45m \
	./pkg/mempool/... \
	./pkg/networking/... \
	./internal/alerting/... \
	./pkg/contracts/... \
	./pkg/state/... \
	./pkg/reputation/...

echo "OK: race-hot-packages finished"
