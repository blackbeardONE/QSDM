//go:build js && wasm
// +build js,wasm

package monitoring

// wasmBackend is the static, build-tag-determined identifier of
// the WASMSDK backend compiled into this binary.
//
// The js && wasm target is the browser-side QSDM client (see
// pkg/wasm/wasm.go); the WASMSDK in that build is a no-op shim
// because preflight + execution happen via syscall/js. We expose
// "browser_stub" so the qsdm_binary_capabilities metric stays
// well-defined when someone scrapes a browser build (rare, but
// useful for early-stage in-browser benchmarks).
const wasmBackend = "browser_stub"
