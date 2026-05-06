//go:build !js || !wasm
// +build !js !wasm

package monitoring

// wasmBackend is the static, build-tag-determined identifier of
// the WASMSDK backend compiled into this binary.
//
// Native targets (linux/windows/darwin/freebsd, amd64/arm64) all
// match the !js || !wasm constraint and pick up sdk_wazero.go
// since wasm Stage B (2026-05-06). The symmetric file
// build_capabilities_wasm_browser.go labels the unusual js+wasm
// browser target as "browser_stub" for completeness.
const wasmBackend = "wazero"
