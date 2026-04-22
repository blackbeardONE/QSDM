#!/usr/bin/env bash
# Build wasm_module (Rust) for wasm32-unknown-unknown — used by CI and local preflight smoke tests.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MOD="$ROOT/source/wasm_module"
cd "$MOD"
cargo build --release --target wasm32-unknown-unknown
OUT="$MOD/target/wasm32-unknown-unknown/release/wasm_module.wasm"
echo "OK: $OUT"
ls -la "$OUT"
