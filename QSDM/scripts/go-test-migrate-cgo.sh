#!/usr/bin/env bash
# Run cmd/migrate CGO tests. Requires liboqs at QSDM/liboqs_install (same as CI / rebuild_liboqs.sh),
# because CGO=1 compiles pkg/crypto as a transitive dependency of storage.
# From monorepo root: bash QSDM/scripts/go-test-migrate-cgo.sh
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
QSDM_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SOURCE_DIR="$QSDM_ROOT/source"
LIB_ROOT="$QSDM_ROOT/liboqs_install"
if [ ! -f "$LIB_ROOT/include/oqs/oqs.h" ]; then
	echo "liboqs headers not found under $LIB_ROOT. Build with: bash QSDM/scripts/rebuild_liboqs.sh (from QSDM/)." >&2
	exit 1
fi
if [ -d "$LIB_ROOT/lib64" ]; then L="$LIB_ROOT/lib64"; else L="$LIB_ROOT/lib"; fi

unset CGO_CFLAGS CGO_LDFLAGS 2>/dev/null || true
export CGO_ENABLED=1
export QSDMPLUS_METRICS_REGISTER_STRICT=1
export CGO_CFLAGS="-I${LIB_ROOT}/include"
export CGO_LDFLAGS="-L${L} -loqs"
export LD_LIBRARY_PATH="${L}:${LD_LIBRARY_PATH:-}"

cd "$SOURCE_DIR"
go test ./cmd/migrate/... -count=1 -short -timeout 2m

echo "OK: go-test-migrate-cgo finished"
