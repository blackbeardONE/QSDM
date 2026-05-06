//go:build wasm_wazero
// +build wasm_wazero

package wasm

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

// Stage A parity tests for the wazero-backed WASMSDK
// (sdk_wazero.go). These run only under `-tags wasm_wazero`.
// Each test asserts a specific behavioural contract that
// downstream callers (cmd/qsdm/main.go, pkg/contracts/engine.go,
// pkg/wasm/preflight.go) rely on, so the rollout from
// opt-in (Stage A) to default (Stage B) does not surprise
// anyone with a regression that only the WASM path would
// expose.

// minimalAddWASM_sdk is a verbatim copy of runtime_test.go's
// minimalAddWASM so each *_test.go compilation unit is
// self-contained under build-tag selection. Keeping a copy
// avoids accidental coupling between tests.
//
// Module exports `add(i32, i32) -> i32` returning i32.
var minimalAddWASM_sdk = []byte{
	0x00, 0x61, 0x73, 0x6d,
	0x01, 0x00, 0x00, 0x00,
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x07, 0x01, 0x03, 0x61, 0x64, 0x64, 0x00, 0x00,
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b,
}

// TestWazeroSDK_RoundTrip_AddJSON verifies the contracts-engine
// hot path: NewWASMSDK -> CallFunction(name, jsonString) ->
// numeric result. pkg/contracts/engine.go:277 always passes a
// JSON-encoded array of args as a single string param.
func TestWazeroSDK_RoundTrip_AddJSON(t *testing.T) {
	sdk, err := NewWASMSDK(minimalAddWASM_sdk)
	if err != nil {
		t.Fatalf("NewWASMSDK: %v", err)
	}
	defer sdk.Close()

	out, err := sdk.CallFunction("add", "[3, 5]")
	if err != nil {
		t.Fatalf("CallFunction add: %v", err)
	}
	got, ok := out.(uint64)
	if !ok {
		t.Fatalf("result type = %T, want uint64", out)
	}
	if int32(got) != 8 {
		t.Errorf("add(3,5) = %d, want 8", int32(got))
	}
}

// TestWazeroSDK_StubFlagNotMarked guards the operational
// invariant that under -tags wasm_wazero the SDK is real and
// qsdm_stub_active{kind="wasm_sdk"} stays at 0. The opposite
// invariant — flag flips when stub backends fail — is
// already covered by TestWasmSDK_StubActiveIsLazy in
// sdk_stubactive_test.go (which is build-tag-agnostic and
// uses t.Skip when a real SDK constructs).
func TestWazeroSDK_StubFlagNotMarked(t *testing.T) {
	stubactive.Reset()
	defer stubactive.Reset()

	sdk, err := NewWASMSDK(minimalAddWASM_sdk)
	if err != nil {
		t.Fatalf("NewWASMSDK: %v", err)
	}
	defer sdk.Close()

	if stubactive.Active(stubactive.KindWasmSDK) {
		t.Errorf("qsdm_stub_active{kind=%q} unexpectedly true under "+
			"-tags wasm_wazero — wazero backend should never mark "+
			"the stub active", stubactive.KindWasmSDK)
	}
}

// TestWazeroSDK_EmptyBytecodeRejected pins the construction
// contract: an empty byte slice is rejected at NewWASMSDK,
// matching the prior stub builds' "always-error" outward
// behaviour for invalid input. Callers that handle this
// error today (cmd/qsdm/main.go:546) continue to behave
// identically.
func TestWazeroSDK_EmptyBytecodeRejected(t *testing.T) {
	sdk, err := NewWASMSDK(nil)
	if err == nil {
		_ = sdk.Close()
		t.Fatal("NewWASMSDK(nil): err = nil; want error on empty bytecode")
	}
	sdk, err = NewWASMSDK([]byte{})
	if err == nil {
		_ = sdk.Close()
		t.Fatal("NewWASMSDK(empty): err = nil; want error on empty bytecode")
	}
}

// TestWazeroSDK_CallFunctionUnknownExport asserts that calling
// an undefined export returns an error rather than panicking
// or silently succeeding — important because the contracts
// engine uses CallFunction to dispatch user-defined contract
// functions whose names are external input.
func TestWazeroSDK_CallFunctionUnknownExport(t *testing.T) {
	sdk, err := NewWASMSDK(minimalAddWASM_sdk)
	if err != nil {
		t.Fatalf("NewWASMSDK: %v", err)
	}
	defer sdk.Close()

	_, err = sdk.CallFunction("does_not_exist", "[]")
	if err == nil {
		t.Fatal("CallFunction(unknown): err = nil; want error")
	}
}

// TestWazeroSDK_PreflightNoValidator asserts the preflight
// shortcut: a module with no `validate_raw` export is treated
// as having no preflight rules, so the gossip pipeline does
// not drop messages. preflight.go::TryPreflightP2PTransactionJSON
// already has a nil-SDK fast path; the wazero SDK contributes
// the no-export fast path.
func TestWazeroSDK_PreflightNoValidator(t *testing.T) {
	sdk, err := NewWASMSDK(minimalAddWASM_sdk) // no validate_raw export
	if err != nil {
		t.Fatalf("NewWASMSDK: %v", err)
	}
	defer sdk.Close()

	ok, err := sdk.preflightP2PTransactionJSON([]byte(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("preflightP2PTransactionJSON: %v", err)
	}
	if !ok {
		t.Errorf("preflight on a module without validate_raw must " +
			"accept; the gossip layer has no other validator to fall " +
			"back to and dropping by default would silently break " +
			"transaction propagation")
	}
}
