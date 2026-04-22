#!/usr/bin/env bash
# Lightweight local checks complementing CI Trivy/govulncheck. Run from QSDM/source.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/source"

echo "==> go mod verify"
go mod verify

if [ "${SKIP_GOVULNCHECK:-}" = "1" ]; then
	echo "SKIP: govulncheck (SKIP_GOVULNCHECK=1)"
else
	echo "==> govulncheck (set SKIP_GOVULNCHECK=1 to skip)"
	export CGO_ENABLED=0
	unset CGO_CFLAGS CGO_LDFLAGS 2>/dev/null || true
	export QSDMPLUS_METRICS_REGISTER_STRICT=1
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...
fi

echo "OK: security-local-check finished"
