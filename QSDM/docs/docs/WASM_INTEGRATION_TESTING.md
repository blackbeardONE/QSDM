# WASM Integration Testing Documentation

## Overview

This document describes the testing strategy for Go-compiled WASM modules in the QSDM project.

## Go WASM Modules

The wallet and validator WASM modules are compiled from Go source code and depend on the Go runtime environment (`gojs` namespace).

## Testing Challenges

- Go WASM modules require the official Go WASM runtime JavaScript (`go.wasm.js`) to provide necessary imports.
- Generic WASI runtimes like Wasmer do not support Go runtime imports, causing instantiation failures.
- Native Wasmer Go bindings cannot run Go WASM modules due to missing Go runtime imports.
- Node.js environment lacks native support for the Go WASM runtime without a browser-like environment.

## Recommended Testing Approach

### Browser-Based Testing

- Use the provided browser test harness `tests/wasm_js_integration_browser_test_go_official.html`.
- This HTML loads the official Go WASM runtime JS and runs the Go WASM modules in a browser environment.
- It provides proper runtime support and allows interactive debugging.

### Node.js Testing

- Running Go WASM modules in Node.js requires a headless browser environment or a JS runtime that supports the Go WASM runtime.
- A simple Node.js test script is not sufficient due to missing runtime imports.
- For now, use the browser test harness or automate browser testing with tools like Puppeteer or Playwright.

## Scripts and Files

- `tests/wasm_js_integration_browser_test_go_official.html`: Browser test harness for Go WASM modules.
- `tests/wasm_js_integration_test_node_final.js`: Placeholder Node.js test script indicating the need for a browser environment.
- `tests/go_wasm_runtime.js`: Minimal Go WASM runtime wrapper for Node.js (limited support).
- WASM modules located in `wasm_modules/wallet/` and `wasm_modules/validator/`.

## Future Work

- Automate browser-based testing using headless browsers.
- Improve Node.js runtime support for Go WASM modules if feasible.
- Enhance logging and diagnostics in test harnesses.

## Summary

Due to Go runtime dependencies, Go-compiled WASM modules must be tested in environments that provide the official Go WASM runtime JS, primarily browsers.

This approach ensures compatibility and reliable testing results.

---

Developed by Blackbeard | Ten Titanics | GitHub: blackbeardONE
