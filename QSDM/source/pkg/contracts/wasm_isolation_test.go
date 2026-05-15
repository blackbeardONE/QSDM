package contracts

import (
	"context"
	"testing"
)

// Engine-level WASM sandbox isolation tests — closes the
// engine-wiring side of the sc-01 audit-checklist row ("Verify
// wazero sandboxes provide memory isolation between contracts
// (no shared state leaks)"). The wazero-level tests in
// pkg/wasm/isolation_test.go cover the underlying Wasm
// memory-isolation guarantee; this file covers the
// ContractEngine plumbing that decides which runtime serves
// which contract.
//
// The engine wires per-contract isolation in three places:
//
//   1. DeployContract (engine.go:132-137): if the deployed code
//      starts with the Wasm magic header \0asm, the engine
//      creates a BRAND-NEW wasm.NewWazeroRuntime and stores it
//      under the contract ID (ce.contractRTs[contractID] = rt).
//      A wiring bug that reused a single runtime across
//      contracts would defeat all the per-instance memory
//      isolation wazero provides.
//
//   2. executeWASMMetered (engine.go:250-258): the function
//      lookup is keyed by contract.ID (rt, ok :=
//      ce.contractRTs[contract.ID]; ok && rt.HasFunction(...)).
//      A wiring bug that fell through to the shared wazeroRT or
//      a stale contractRTs entry would let a malicious caller
//      reach another contract's runtime by spoofing function
//      names.
//
//   3. simulateExecution fallback (engine.go:284-289): only
//      runs when no wazero runtime is wired and operates on
//      per-contract Contract.State maps — also per-contract
//      isolated by construction, but not the sc-01 attack
//      surface.

// isolationModuleWASM is the same hand-assembled module used by
// pkg/wasm/isolation_test.go: exports memory + set(i32) +
// get() -> i32 over a single page of linear memory. Wire-format
// details are documented in that file's leading comment.
var isolationModuleWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// type section
	0x01, 0x09, 0x02, 0x60, 0x01, 0x7f, 0x00, 0x60, 0x00, 0x01, 0x7f,
	// function section
	0x03, 0x03, 0x02, 0x00, 0x01,
	// memory section
	0x05, 0x03, 0x01, 0x00, 0x01,
	// export section
	0x07, 0x16, 0x03,
	0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00,
	0x03, 0x73, 0x65, 0x74, 0x00, 0x00,
	0x03, 0x67, 0x65, 0x74, 0x00, 0x01,
	// code section
	0x0a, 0x13, 0x02,
	0x09, 0x00, 0x41, 0x00, 0x20, 0x00, 0x36, 0x02, 0x00, 0x0b,
	0x07, 0x00, 0x41, 0x00, 0x28, 0x02, 0x00, 0x0b,
}

func isolationTestABI() *ABI {
	return &ABI{
		Functions: []Function{
			{Name: "set", Inputs: []Param{{Name: "value", Type: "i32"}}, Outputs: nil, StateMutating: true},
			{Name: "get", Inputs: nil, Outputs: []Param{{Name: "result", Type: "i32"}}},
		},
	}
}

// TestContractEngine_PerContract_RuntimeIsolation_NoCrossLeak is
// the headline sc-01 engine-wiring test. We deploy two
// contracts with byte-identical WASM bytecode (same module,
// same exports, same linear-memory layout), then drive their
// per-contract runtimes directly through ce.contractRTs to
// write distinct values into each one's memory. If the engine
// is correctly maintaining per-contract isolation, contract A's
// memory still holds A's write after contract B has overwritten
// the same memory address in B. If the engine reused a single
// runtime (or one runtime's memory aliased the other), B's
// write would clobber A's view.
//
// We deliberately bypass ExecuteContract here because that
// method's argsJSON plumbing is shaped for map[string]interface{}
// payloads (engine.go marshals to a JSON object, while
// wasm.WazeroRuntime.Call expects a JSON array of numbers).
// Driving the per-contract runtime via ce.contractRTs preserves
// the test's focus on the isolation invariant: the question
// "does contract A see contract B's writes" is independent of
// how arguments are encoded at the engine→runtime boundary.
func TestContractEngine_PerContract_RuntimeIsolation_NoCrossLeak(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()

	contractA, err := engine.DeployContract(ctx, "contract-A", isolationModuleWASM, isolationTestABI(), "owner-A")
	if err != nil {
		t.Fatalf("DeployContract A: %v", err)
	}
	if contractA == nil {
		t.Fatal("DeployContract A returned nil contract")
	}

	contractB, err := engine.DeployContract(ctx, "contract-B", isolationModuleWASM, isolationTestABI(), "owner-B")
	if err != nil {
		t.Fatalf("DeployContract B: %v", err)
	}
	if contractB == nil {
		t.Fatal("DeployContract B returned nil contract")
	}

	rtA := engine.contractRTs["contract-A"]
	rtB := engine.contractRTs["contract-B"]
	if rtA == nil {
		t.Fatal("engine.contractRTs missing entry for contract-A; DeployContract did not instantiate a per-contract runtime")
	}
	if rtB == nil {
		t.Fatal("engine.contractRTs missing entry for contract-B; DeployContract did not instantiate a per-contract runtime")
	}
	if rtA == rtB {
		t.Fatal("engine.contractRTs[A] == engine.contractRTs[B]; two contracts share the same runtime — per-contract isolation is broken")
	}

	if _, err := rtA.Call("set", []byte("[42]")); err != nil {
		t.Fatalf("rtA.Call(set, 42): %v", err)
	}
	if _, err := rtB.Call("set", []byte("[99]")); err != nil {
		t.Fatalf("rtB.Call(set, 99): %v", err)
	}

	gotA, err := rtA.Call("get", nil)
	if err != nil {
		t.Fatalf("rtA.Call(get): %v", err)
	}
	gotB, err := rtB.Call("get", nil)
	if err != nil {
		t.Fatalf("rtB.Call(get): %v", err)
	}

	a, ok := gotA.(uint64)
	if !ok {
		t.Fatalf("contract-A get result type: got %T, want uint64", gotA)
	}
	b, ok := gotB.(uint64)
	if !ok {
		t.Fatalf("contract-B get result type: got %T, want uint64", gotB)
	}

	if a != 42 {
		t.Errorf("contract-A read after isolation: got %d, want 42; if equal to 99 then contract-B's memory leaked into A", a)
	}
	if b != 99 {
		t.Errorf("contract-B read after isolation: got %d, want 99; if equal to 42 then contract-A's memory leaked into B", b)
	}
}

// TestContractEngine_DeployContract_FreshRuntimePerContract
// guards a narrower structural invariant: every WASM-headed
// DeployContract call must produce a brand-new entry in
// ce.contractRTs, never a reuse. We deploy three contracts in
// a row, all with the same code, and assert the three runtime
// pointers are mutually distinct.
//
// A regression that flipped DeployContract to reuse a single
// engine-wide runtime (for example by promoting the first
// rt into ce.wazeroRT) would silently pass the linear-memory
// test above (it reads through ce.contractRTs which would be
// empty or aliased) but break this one.
func TestContractEngine_DeployContract_FreshRuntimePerContract(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()

	ids := []string{"contract-1", "contract-2", "contract-3"}
	for _, id := range ids {
		if _, err := engine.DeployContract(ctx, id, isolationModuleWASM, isolationTestABI(), "owner"); err != nil {
			t.Fatalf("DeployContract %s: %v", id, err)
		}
		if rt := engine.contractRTs[id]; rt == nil {
			t.Fatalf("DeployContract did not create a runtime for %s", id)
		}
	}

	// Pointer-identity comparison on *wasm.WazeroRuntime: every
	// DeployContract on Wasm-headed code must produce a fresh
	// runtime, never a reuse. A regression that promoted the
	// first contract's rt into ce.wazeroRT or that flipped
	// contractRTs to a single shared entry would alias these
	// pointers.
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if engine.contractRTs[ids[i]] == engine.contractRTs[ids[j]] {
				t.Errorf("contractRTs[%s] and contractRTs[%s] alias the same *WazeroRuntime; DeployContract is reusing runtimes across contracts", ids[i], ids[j])
			}
		}
	}
}

// TestContractEngine_DeployContract_NonWASMSkipsRuntime is a
// negative companion to the isolation tests. Non-WASM code
// (anything without the \0asm magic header) must NOT cause a
// runtime to be wired — the simulation fallback path is the
// only legitimate execution route for that contract. A
// regression that wired a runtime for arbitrary bytes could
// leak memory address space across contracts because the
// runtime would compile arbitrary input as Wasm, which wazero
// rejects but the engine's error-tolerant DeployContract
// (engine.go:134: "if rtErr == nil") silently ignores.
func TestContractEngine_DeployContract_NonWASMSkipsRuntime(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()

	// Plain-text "code" — definitely not a Wasm module.
	if _, err := engine.DeployContract(ctx, "plain-text", []byte("not a wasm module"), &ABI{}, "owner"); err != nil {
		t.Fatalf("DeployContract: %v", err)
	}
	if rt, ok := engine.contractRTs["plain-text"]; ok {
		t.Errorf("non-WASM code unexpectedly got a runtime instance: %p; engine should have left contractRTs empty", rt)
	}

	// Short payload that happens to start with 4 bytes that look
	// like Wasm but is truncated — should fail to compile and not
	// be wired.
	if _, err := engine.DeployContract(ctx, "truncated", []byte{0x00, 0x61, 0x73, 0x6d, 0x01}, &ABI{}, "owner"); err != nil {
		t.Fatalf("DeployContract: %v", err)
	}
	if rt, ok := engine.contractRTs["truncated"]; ok {
		t.Errorf("truncated-WASM code unexpectedly got a runtime instance: %p; engine should have left contractRTs empty", rt)
	}
}

