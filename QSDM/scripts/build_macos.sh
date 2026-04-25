#!/bin/bash
# Build QSDM on macOS (Intel + Apple Silicon) with liboqs-enabled CGO.
#
# Looks for liboqs in common locations (./liboqs_install first), falls back to
# rebuild_liboqs_macos.sh, and links against Homebrew OpenSSL when present.
#
# Usage:
#   ./scripts/build_macos.sh                 # builds ./qsdm
#   QSDM_NO_CGO=1 ./scripts/build_macos.sh   # pure-Go fallback build

set -euo pipefail

echo "=== Building QSDM on macOS ==="
echo ""

if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "ERROR: this script is macOS only. Use scripts/build.sh on Linux or build.ps1 on Windows."
    exit 1
fi

if ! command -v go >/dev/null 2>&1; then
    echo "ERROR: Go not found. Install via: brew install go"
    exit 1
fi

echo "Go: $(go version)"
echo "Arch: $(uname -m)"
echo ""

if [[ "${QSDM_NO_CGO:-0}" == "1" ]]; then
    echo "QSDM_NO_CGO=1 — skipping liboqs / CGO wiring."
    export CGO_ENABLED=0
    unset CGO_CFLAGS CGO_LDFLAGS CGO_CPPFLAGS CGO_CXXFLAGS || true
    go build -o ./qsdm -v ./cmd/qsdm
    echo ""
    echo "Built no-CGO binary: ./qsdm"
    exit 0
fi

export CGO_ENABLED=1
unset CGO_CFLAGS CGO_LDFLAGS CGO_CPPFLAGS CGO_CXXFLAGS || true

POSSIBLE_PATHS=(
    "$(pwd)/liboqs_install"
    "$HOME/liboqs_install"
    "/usr/local"
    "/opt/liboqs"
    "/opt/homebrew"
)

LIBOQS_PATH=""
for path in "${POSSIBLE_PATHS[@]}"; do
    if [[ -f "${path}/include/oqs/oqs.h" ]]; then
        LIBOQS_PATH="${path}"
        break
    fi
done

if [[ -z "${LIBOQS_PATH}" ]]; then
    echo "liboqs not found — running rebuild_liboqs_macos.sh..."
    bash "$(dirname "$0")/rebuild_liboqs_macos.sh"
    if [[ -f "$(pwd)/liboqs_install/include/oqs/oqs.h" ]]; then
        LIBOQS_PATH="$(pwd)/liboqs_install"
    else
        echo "ERROR: liboqs install still missing after rebuild." >&2
        exit 1
    fi
fi

echo "liboqs: ${LIBOQS_PATH}"

export CGO_CFLAGS="-I${LIBOQS_PATH}/include"
if [[ -f "${LIBOQS_PATH}/lib/liboqs.dylib" || -f "${LIBOQS_PATH}/lib/liboqs.a" ]]; then
    export CGO_LDFLAGS="-L${LIBOQS_PATH}/lib -loqs"
    export DYLD_LIBRARY_PATH="${LIBOQS_PATH}/lib:${DYLD_LIBRARY_PATH:-}"
elif [[ -f "${LIBOQS_PATH}/lib64/liboqs.dylib" || -f "${LIBOQS_PATH}/lib64/liboqs.a" ]]; then
    export CGO_LDFLAGS="-L${LIBOQS_PATH}/lib64 -loqs"
    export DYLD_LIBRARY_PATH="${LIBOQS_PATH}/lib64:${DYLD_LIBRARY_PATH:-}"
else
    export CGO_LDFLAGS="-loqs"
fi

# Add Homebrew OpenSSL to include/lib path when present.
if command -v brew >/dev/null 2>&1; then
    OPENSSL_ROOT="$(brew --prefix openssl@3 2>/dev/null || true)"
    if [[ -n "${OPENSSL_ROOT}" && -d "${OPENSSL_ROOT}/include" ]]; then
        export CGO_CFLAGS="${CGO_CFLAGS} -I${OPENSSL_ROOT}/include"
        export CGO_LDFLAGS="${CGO_LDFLAGS} -L${OPENSSL_ROOT}/lib"
    fi
fi

echo ""
echo "CGO_CFLAGS:  ${CGO_CFLAGS}"
echo "CGO_LDFLAGS: ${CGO_LDFLAGS}"
echo ""

# Work from source/ if executed from repo root.
if [[ -f "source/go.mod" ]]; then
    cd source
    go build -o ../qsdm -v ./cmd/qsdm
    cd ..
elif [[ -f "go.mod" ]]; then
    go build -o ../qsdm -v ./cmd/qsdm
else
    echo "ERROR: go.mod not found. Run from QSDM root or source/."
    exit 1
fi

echo ""
echo "=== Build successful ==="
echo "Binary: ./qsdm"
echo ""
echo "Runtime linkage:"
if command -v otool >/dev/null 2>&1 && [[ -f ./qsdm ]]; then
    otool -L ./qsdm | head -10 || true
fi
echo ""
echo "To run:"
echo "  export DYLD_LIBRARY_PATH=\"${LIBOQS_PATH}/lib:\${DYLD_LIBRARY_PATH:-}\""
echo "  ./qsdm"
