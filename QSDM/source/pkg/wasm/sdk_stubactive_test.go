package wasm

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

// TestWasmSDK_StubActiveIsLazy guards the operational invariant
// that qsdm_stub_active{kind="wasm_sdk"} stays at 0 unless an
// operator actually attempts to construct a WASM SDK. The
// alternative — flipping the flag in package init() — would page
// on-call for every non-CGO build (and for every CGO build
// missing the wasmtime DLLs) regardless of whether WASM modules
// are configured, drowning the dangerous-stub alert in benign
// noise.
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

	// Now exercise the stub. NewWASMSDK is expected to return an
	// error in both stub builds (sdk_stub.go for !cgo,
	// sdk_wasmtime_disabled.go for cgo without wasmtime DLLs).
	// In a production CGO+wasmtime build this test is irrelevant
	// (the SDK constructs successfully and never marks the
	// stubactive flag); skip when we observe success.
	sdk, err := NewWASMSDK([]byte("ignored"))
	if err == nil {
		// Real wasmtime-backed build. Stub semantics don't
		// apply here; nothing to assert.
		_ = sdk
		t.Skip("real wasmtime-backed SDK constructed successfully; " +
			"stubactive lazy-flag invariant only applies to stub builds")
	}

	if !stubactive.Active(stubactive.KindWasmSDK) {
		t.Errorf("qsdm_stub_active{kind=%q} not set after failed NewWASMSDK; "+
			"stub-active flag is supposed to flip on attempted use",
			stubactive.KindWasmSDK)
	}
}
