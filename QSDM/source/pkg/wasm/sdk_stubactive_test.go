package wasm

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

// minimalAddWASM_lazy is a self-contained 39-byte WASM module
// exporting `add(i32,i32)->i32`. Kept as a separate copy from
// runtime_test.go's minimalAddWASM and sdk_wazero_test.go's
// minimalAddWASM_sdk so this test compiles under every build
// tag combination (stub-only, wasm_wazero, real wasmtime).
var minimalAddWASM_lazy = []byte{
	0x00, 0x61, 0x73, 0x6d,
	0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x07, 0x01, 0x03, 0x61, 0x64, 0x64, 0x00, 0x00,
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b,
}

// TestWasmSDK_StubActiveIsLazy guards the operational invariant
// that qsdm_stub_active{kind="wasm_sdk"} stays at 0 unless an
// operator actually attempts to construct a WASM SDK. The
// alternative — flipping the flag in package init() — would page
// on-call for every non-CGO build (and for every CGO build
// missing the wasmtime DLLs) regardless of whether WASM modules
// are configured, drowning the dangerous-stub alert in benign
// noise.
//
// Build-tag matrix this test must work under:
//
//   - default !cgo (sdk_stub.go) — stub backend; valid WASM
//     bytes still error, flag flips. ASSERT both.
//   - default cgo, no wasmtime DLLs (sdk_wasmtime_disabled.go)
//     — stub backend; same as above. ASSERT both.
//   - wasm_wazero (sdk_wazero.go) — real backend; valid WASM
//     bytes succeed, flag does not flip. SKIP (no stub
//     semantics to assert).
//   - cgo + real wasmtime DLLs — real backend; same as
//     wasm_wazero case. SKIP.
//
// We distinguish "stub" from "real" by feeding a valid 39-byte
// add module and checking whether construction succeeds.
//
// We rely on stubactive.Reset() being a test-only helper; the
// production binary never calls it, so the test isolates the
// stubactive registry from any other init() side effects.
func TestWasmSDK_StubActiveIsLazy(t *testing.T) {
	stubactive.Reset()
	defer stubactive.Reset()

	if stubactive.Active(stubactive.KindWasmSDK) {
		t.Fatalf("qsdm_stub_active{kind=%q} unexpectedly true after package "+
			"load — wasm.NewWASMSDK should be the only place that flips it",
			stubactive.KindWasmSDK)
	}

	sdk, err := NewWASMSDK(minimalAddWASM_lazy)
	if err == nil {
		// Real backend (wazero, or real wasmtime). Stub
		// semantics don't apply; the lazy-flag invariant is
		// covered by sdk_wazero_test.go::TestWazeroSDK_StubFlagNotMarked
		// when the wazero backend is in scope.
		_ = sdk
		t.Skip("real WASM backend constructed successfully; " +
			"stubactive lazy-flag invariant only applies to stub builds")
	}

	if !stubactive.Active(stubactive.KindWasmSDK) {
		t.Errorf("qsdm_stub_active{kind=%q} not set after failed NewWASMSDK; "+
			"stub-active flag is supposed to flip on attempted use",
			stubactive.KindWasmSDK)
	}
}
