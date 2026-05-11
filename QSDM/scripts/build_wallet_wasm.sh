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
SRI_ONLY=0
for arg in "$@"; do
    case "$arg" in
        --skip-runtime)    SKIP_RUNTIME=1 ;;
        # Refresh the SRI hashes in wallet.html / wallet.js *without*
        # rebuilding wallet.wasm or copying wasm_exec.js. Useful when
        # you've only edited the JS or HTML and want the integrity
        # values to track the new bytes without a 30-second WASM
        # rebuild. The WASM and runtime files on disk are reused as-is.
        --refresh-sri-only) SRI_ONLY=1 ;;
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

if [[ "$SRI_ONLY" -eq 1 ]]; then
    # Fast path: skip the Go build entirely and jump straight to the
    # SRI refresh below. Useful when only the HTML / JS source changed
    # and the WASM / runtime files on disk are still authoritative.
    if [[ ! -f "$OUT_WASM" || ! -f "$OUT_EXEC" ]]; then
        echo "ERROR: --refresh-sri-only requires existing $OUT_WASM and $OUT_EXEC" >&2
        echo "       Run without the flag first to build them." >&2
        exit 1
    fi
    echo "==> --refresh-sri-only: skipping WASM build, reusing on-disk artefacts"
else
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
fi

if [[ "$SKIP_RUNTIME" -eq 0 && "$SRI_ONLY" -eq 0 ]]; then
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

# --------------------------------------------------------------------
# Refresh Subresource Integrity (SRI) hashes.
#
# Three sha384 hashes have to stay in sync with the bytes that just
# landed in $OUT_DIR:
#
#   wallet.html  →  integrity="..." for wasm_exec.js
#   wallet.html  →  integrity="..." for wallet.js
#   wallet.js    →  integrity: '...' inside fetch('/wallet.wasm')
#
# Dependency order matters: editing wallet.js (to embed the new
# wallet.wasm hash) changes wallet.js's own bytes, so wallet.html's
# integrity for wallet.js must be computed *after* the wallet.js edit.
# We do them in that order below.
#
# If openssl is missing (extremely unusual on a build host that
# already has Go) we skip the refresh and emit a loud warning so the
# operator runs it by hand before publishing — committing stale SRI
# would break the wallet at the browser fetch step with no server-side
# signal.
# --------------------------------------------------------------------
update_sri_hashes() {
    if ! command -v openssl >/dev/null 2>&1; then
        echo "WARNING: openssl not on PATH; skipping SRI hash refresh" >&2
        echo "         The committed wallet.html / wallet.js integrity values" >&2
        echo "         are now STALE relative to the freshly-built wallet.wasm." >&2
        echo "         Re-run this script after installing openssl, or compute" >&2
        echo "         sha384 base64 hashes by hand and update them." >&2
        return 0
    fi

    local html_file="$OUT_DIR/wallet.html"
    local js_file="$OUT_DIR/wallet.js"
    local wasm_file="$OUT_WASM"
    local exec_file="$OUT_EXEC"

    # sha384_b64 <file> → "sha384-<base64>"; refuses to print anything
    # other than the value (no newline, no labels) so a downstream sed
    # substitution can splice the result in directly.
    sha384_b64() {
        printf 'sha384-%s' "$(openssl dgst -sha384 -binary "$1" | openssl base64 -A)"
    }

    local exec_sri wasm_sri js_sri
    exec_sri="$(sha384_b64 "$exec_file")"
    wasm_sri="$(sha384_b64 "$wasm_file")"

    # 1) wallet.js: pin the wallet.wasm hash inside the fetch call.
    #    The line is unique in the file — there is exactly one place
    #    that fetches /wallet.wasm — so a global substitution is safe.
    sed -i.bak -E \
        "s#integrity: 'sha384-[A-Za-z0-9+/=]+'#integrity: '$wasm_sri'#g" \
        "$js_file"
    rm -f "$js_file.bak"

    # 2) wallet.js byte image has now changed; recompute its hash for
    #    the wallet.html substitution that follows.
    js_sri="$(sha384_b64 "$js_file")"

    # 3) wallet.html: refresh both <script integrity="…"> attributes.
    #    Each substitution is anchored on the script's src= attribute
    #    so the two integrity values don't collide.
    sed -i.bak -E \
        -e "s#(src=\"/wasm_exec\\.js\" integrity=\")sha384-[A-Za-z0-9+/=]+(\")#\\1$exec_sri\\2#" \
        -e "s#(src=\"/wallet\\.js\" integrity=\")sha384-[A-Za-z0-9+/=]+(\")#\\1$js_sri\\2#" \
        "$html_file"
    rm -f "$html_file.bak"

    # Sanity: confirm every placeholder was actually rewritten. A
    # missing match would silently leave a stale or template value in
    # place; we'd rather fail the build than ship that.
    if grep -q 'WALLETJS_HASH_PLACEHOLDER' "$html_file" \
       || ! grep -q "$js_sri"   "$html_file" \
       || ! grep -q "$exec_sri" "$html_file" \
       || ! grep -q "$wasm_sri" "$js_file"; then
        echo "ERROR: SRI refresh did not produce expected hashes" >&2
        echo "       html_file=$html_file" >&2
        echo "       js_file=$js_file" >&2
        echo "       expected wasm sri=$wasm_sri" >&2
        echo "       expected js   sri=$js_sri" >&2
        echo "       expected exec sri=$exec_sri" >&2
        exit 1
    fi

    echo "==> SRI hashes refreshed:"
    echo "    wallet.wasm  → $wasm_sri"
    echo "    wallet.js    → $js_sri"
    echo "    wasm_exec.js → $exec_sri"
}

update_sri_hashes

echo "==> Done. Open QSDM/deploy/landing/wallet.html in a static-file server"
echo "    (e.g. \`python3 -m http.server -d QSDM/deploy/landing 8088\`) to test locally."
