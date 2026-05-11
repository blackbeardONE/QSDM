#!/usr/bin/env bash
# build_wallet_wasm.sh — compile the browser wallet's Go→WebAssembly
# entry point and copy the matching `wasm_exec.js` runtime alongside it
# so the static page (deploy/landing/wallet.html) can load them with
# zero further server setup.
#
# Output (after a successful run):
#
#   QSDM/deploy/landing/wallet.wasm       Go-WASM binary (~3 MB)
#   QSDM/deploy/landing/wasm_exec.js      Go runtime shim (copied from $GOROOT)
#
# Both files are committed to the repo so a fresh clone of the landing
# site is immediately serveable without a build step on the deploy host.
# Rebuild this script when:
#
#   - The Go toolchain version changes (wasm_exec.js is toolchain-pinned).
#   - wasm_modules/wallet/cmd/qsdm-wallet/main.go changes.
#   - cloudflare/circl gets a security update (force a clean WASM rebuild
#     so the new mldsa87 implementation lands in the served binary).
#
# Usage:
#
#   ./QSDM/scripts/build_wallet_wasm.sh           # build + copy runtime
#   ./QSDM/scripts/build_wallet_wasm.sh --skip-runtime
#
# The --skip-runtime flag suppresses the wasm_exec.js copy step; useful
# when the runtime is being pinned to a specific upstream commit out of
# band.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
SOURCE_DIR="$REPO_ROOT/QSDM/source"
OUT_DIR="$REPO_ROOT/QSDM/deploy/landing"
OUT_WASM="$OUT_DIR/wallet.wasm"
OUT_EXEC="$OUT_DIR/wasm_exec.js"
ENTRY_PKG="./wasm_modules/wallet/cmd/qsdm-wallet"

SKIP_RUNTIME=0
for arg in "$@"; do
    case "$arg" in
        --skip-runtime) SKIP_RUNTIME=1 ;;
        -h|--help)
            sed -n '1,32p' "$0"
            exit 0
            ;;
        *)
            echo "ERROR: unknown flag $arg" >&2
            exit 2
            ;;
    esac
done

mkdir -p "$OUT_DIR"

# Resolve the Go toolchain. GOROOT may not be exported in non-interactive
# shells (e.g. CI); fall back to `go env` so the script works either way.
if ! command -v go >/dev/null 2>&1; then
    echo "ERROR: go not found on PATH" >&2
    exit 1
fi
GO_VERSION="$(go version | awk '{print $3}')"
GOROOT_VAL="$(go env GOROOT)"

echo "==> Toolchain:    $GO_VERSION ($GOROOT_VAL)"
echo "==> Source pkg:   $SOURCE_DIR/$ENTRY_PKG"
echo "==> Output WASM:  $OUT_WASM"

cd "$SOURCE_DIR"
GOOS=js GOARCH=wasm go build -trimpath -ldflags '-s -w' -o "$OUT_WASM" "$ENTRY_PKG"

WASM_SIZE="$(wc -c <"$OUT_WASM")"
echo "==> Built $WASM_SIZE bytes ($((WASM_SIZE / 1024)) KB)."

if [[ "$SKIP_RUNTIME" -eq 0 ]]; then
    # Go ≥ 1.24 ships wasm_exec.js at $GOROOT/lib/wasm/wasm_exec.js.
    # Older toolchains kept it at $GOROOT/misc/wasm/. Probe both.
    for candidate in "$GOROOT_VAL/lib/wasm/wasm_exec.js" "$GOROOT_VAL/misc/wasm/wasm_exec.js"; do
        if [[ -f "$candidate" ]]; then
            cp "$candidate" "$OUT_EXEC"
            echo "==> Copied $candidate → $OUT_EXEC"
            break
        fi
    done
    if [[ ! -f "$OUT_EXEC" ]]; then
        echo "ERROR: could not locate wasm_exec.js under $GOROOT_VAL" >&2
        echo "       (looked at lib/wasm and misc/wasm)" >&2
        exit 1
    fi
fi

echo "==> Done. Open QSDM/deploy/landing/wallet.html in a static-file server"
echo "    (e.g. \`python3 -m http.server -d QSDM/deploy/landing 8088\`) to test locally."
