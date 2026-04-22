package tests

import (
	"testing"
)

func TestWasmModules(t *testing.T) {
	// WASM integration tests are skipped when wasmer is not available
	// (CGO disabled or wasmer not installed)
	t.Skip("WASM integration tests require wasmer (CGO enabled)")
}
