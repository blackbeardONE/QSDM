#!/bin/bash
# Rebuild liboqs for macOS (Intel + Apple Silicon)
#
# Usage:
#   ./scripts/rebuild_liboqs_macos.sh              # build universal2 (arm64 + x86_64)
#   QSDM_LIBOQS_ARCH=arm64   ./scripts/...         # single arch
#   QSDM_LIBOQS_ARCH=x86_64  ./scripts/...
#
# Produces $(pwd)/liboqs_install with headers + dylib that can be consumed by
# scripts/build_macos.sh via CGO_CFLAGS / CGO_LDFLAGS / DYLD_LIBRARY_PATH.

set -euo pipefail

echo "=== Rebuilding liboqs for macOS ==="
echo ""

if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "ERROR: this script is macOS only. Use rebuild_liboqs.sh on Linux."
    exit 1
fi

# Required tooling — Homebrew is the usual source of cmake + openssl on macOS.
REQUIRED_TOOLS=(cmake git)
MISSING=()
for t in "${REQUIRED_TOOLS[@]}"; do
    command -v "$t" >/dev/null 2>&1 || MISSING+=("$t")
done
if [[ ${#MISSING[@]} -ne 0 ]]; then
    echo "ERROR: missing tools: ${MISSING[*]}"
    echo "Install with Homebrew:"
    echo "  brew install cmake openssl@3"
    exit 1
fi

# Locate OpenSSL — prefer Homebrew's keg-only openssl@3.
OPENSSL_ROOT="${OPENSSL_ROOT:-}"
if [[ -z "${OPENSSL_ROOT}" ]]; then
    if command -v brew >/dev/null 2>&1; then
        OPENSSL_ROOT="$(brew --prefix openssl@3 2>/dev/null || true)"
    fi
fi
if [[ -z "${OPENSSL_ROOT}" || ! -d "${OPENSSL_ROOT}/include/openssl" ]]; then
    echo "ERROR: OpenSSL 3 not found. Install via: brew install openssl@3"
    echo "       or set OPENSSL_ROOT to a valid OpenSSL prefix."
    exit 1
fi
echo "Using OpenSSL at: ${OPENSSL_ROOT}"

INSTALL_DIR="$(pwd)/liboqs_install"
BUILD_DIR="$(pwd)/liboqs_build"
LIBOQS_SRC="liboqs_src"

ARCH="${QSDM_LIBOQS_ARCH:-universal2}"
case "${ARCH}" in
    arm64|x86_64|universal2) ;;
    *)
        echo "ERROR: unsupported QSDM_LIBOQS_ARCH=${ARCH} (expected arm64|x86_64|universal2)"
        exit 1
        ;;
esac
echo "Target architecture: ${ARCH}"

rm -rf "${BUILD_DIR}" "${INSTALL_DIR}"

if [[ ! -d "${LIBOQS_SRC}" ]]; then
    echo "Cloning liboqs..."
    git clone --depth 1 --branch main https://github.com/open-quantum-safe/liboqs.git "${LIBOQS_SRC}"
else
    (cd "${LIBOQS_SRC}" && git pull --ff-only || true)
fi

mkdir -p "${BUILD_DIR}"
cd "${BUILD_DIR}"

CMAKE_ARGS=(
    -DCMAKE_INSTALL_PREFIX="${INSTALL_DIR}"
    -DCMAKE_BUILD_TYPE=Release
    -DBUILD_SHARED_LIBS=ON
    -DOQS_USE_OPENSSL_SHARED=ON
    -DOQS_USE_AES_OPENSSL=ON
    -DOQS_USE_SHA2_OPENSSL=ON
    -DOQS_USE_SHA3_OPENSSL=ON
    -DOQS_BUILD_ONLY_LIB=ON
    -DOQS_ENABLE_SIG_ml_dsa_87=ON
    -DOPENSSL_ROOT_DIR="${OPENSSL_ROOT}"
    -DCMAKE_INSTALL_NAME_DIR="${INSTALL_DIR}/lib"
)

case "${ARCH}" in
    arm64)
        CMAKE_ARGS+=(-DCMAKE_OSX_ARCHITECTURES="arm64")
        ;;
    x86_64)
        CMAKE_ARGS+=(-DCMAKE_OSX_ARCHITECTURES="x86_64")
        # arm64 hosts need to cross-compile explicitly.
        if [[ "$(uname -m)" == "arm64" ]]; then
            CMAKE_ARGS+=(-DCMAKE_SYSTEM_PROCESSOR=x86_64)
        fi
        ;;
    universal2)
        CMAKE_ARGS+=(-DCMAKE_OSX_ARCHITECTURES="arm64;x86_64")
        ;;
esac

echo ""
echo "Configuring liboqs..."
cmake "${CMAKE_ARGS[@]}" "../${LIBOQS_SRC}"

echo ""
echo "Building liboqs..."
cmake --build . -j "$(sysctl -n hw.ncpu)"

echo ""
echo "Installing liboqs..."
cmake --install .

cd ..

echo ""
echo "=== liboqs build complete ==="
echo ""
echo "Install prefix: ${INSTALL_DIR}"
echo "Library:"
ls -lh "${INSTALL_DIR}/lib"/liboqs.* 2>/dev/null || true
echo ""
echo "Sample headers:"
ls -lh "${INSTALL_DIR}/include/oqs/"*.h 2>/dev/null | head -5 || true
echo ""
echo "To use this build, export:"
echo "  export CGO_CFLAGS=\"-I${INSTALL_DIR}/include\""
echo "  export CGO_LDFLAGS=\"-L${INSTALL_DIR}/lib -loqs\""
echo "  export DYLD_LIBRARY_PATH=\"${INSTALL_DIR}/lib:\${DYLD_LIBRARY_PATH:-}\""
echo ""
echo "Or simply run:"
echo "  ./scripts/build_macos.sh"
