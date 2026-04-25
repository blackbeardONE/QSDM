# Building the Rust WASM Module for QSDM

The module exports **`validate_raw(ptr, len) -> i32`** (boolean as 0/1) for Go **wazero** preflight (`QSDM_WASM_PREFLIGHT_MODULE`). It does **not** require wasm-bindgen in the module binary.

## Prerequisites

- Rust toolchain (https://rustup.rs/)
- Target: `rustup target add wasm32-unknown-unknown`

## Build (wazero / CI)

From repo root:

```bash
bash QSDM/scripts/build-wasm-module.sh
```

Artifact: `QSDM/source/wasm_module/target/wasm32-unknown-unknown/release/wasm_module.wasm`

## Optional: wasm-pack / web

For browser bindings you can still use `wasm-pack build --target web`; that is separate from the **wazero** preflight artifact above.

## Notes

- For Windows, ensure you have the necessary build tools installed (e.g., Visual Studio Build Tools).
- Use an SSD for faster build times.
- Disable antivirus during builds if you experience slowdowns.

## References

- Rust-WASM Book: https://rustwasm.github.io/docs/book/
- wasm-pack: https://rustwasm.github.io/wasm-pack/
- wasm-bindgen: https://github.com/rustwasm/wasm-bindgen
