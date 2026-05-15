package wasm

import (
	"testing"
)

// WASM sandbox isolation tests — closes the wazero side of the
// sc-01 audit-checklist row ("Verify wazero sandboxes provide
// memory isolation between contracts (no shared state leaks)").
//
// The isolation guarantee we are proving has three layers:
//
//   1. Wasm spec: each module instance has its own linear
//      memory (Wasm 1.0 §4.5.3). Cross-instance memory access
//      requires explicit shared-memory imports, which the QSDM
//      contract engine never generates.
//   2. wazero implementation: each wazero.Runtime allocates
//      memory in a per-runtime Go slice, and each
//      wazero.Module instantiated within a runtime gets its own
//      memory.Memory wrapper bound to its own backing store.
//   3. ContractEngine wiring: pkg/contracts/engine.go::DeployContract
//      creates a brand-new wasm.NewWazeroRuntime for every
//      contract (ce.contractRTs[contract.ID] = rt) and
//      executeWASMMetered looks up by contract.ID, so contract A
//      can never invoke contract B's runtime by accident.
//
// This file tests layer 1 + 2 (the engine wiring is covered in
// pkg/contracts/wasm_isolation_test.go). The headline test
// deploys two instances of the same module bytecode, writes
// different values into each one's linear memory through the
// module's own set() export, and asserts the values do not
// leak across instances. If memory were shared, both get()
// calls would return the last-write-wins value; here they must
// return the value each instance wrote on its own.

// isolationModuleWASM is a minimal, hand-assembled WebAssembly
// 1.0 module that exports:
//
//   - memory: 1 page (64 KiB) of linear memory
//   - set(value: i32) -> ():  writes value to memory[0..4]
//   - get() -> i32:           reads i32 from memory[0..4]
//
// Wire-format (decoded byte-by-byte, see Wasm 1.0 §5):
//
//   00 61 73 6d          \0asm magic
//   01 00 00 00          version 1
//   ── Type section (id=1) ──
//   01 09                section id, payload size = 9
//      02                vec count = 2
//      60 01 7f 00       type 0:  (i32) -> ()
//      60 00 01 7f       type 1:  ()    -> (i32)
//   ── Function section (id=3) ──
//   03 03                section id, payload size = 3
//      02 00 01          two funcs: func0 uses type 0, func1 uses type 1
//   ── Memory section (id=5) ──
//   05 03                section id, payload size = 3
//      01 00 01          one memory, flags=0 (min only), min = 1 page
//   ── Export section (id=7) ──
//   07 16                section id, payload size = 22
//      03                vec count = 3
//      06 6d 65 6d 6f 72 79  02 00      name="memory", kind=memory, idx=0
//      03 73 65 74           00 00      name="set",    kind=func,   idx=0
//      03 67 65 74           00 01      name="get",    kind=func,   idx=1
//   ── Code section (id=10/0x0a) ──
//   0a 13                section id, payload size = 19
//      02                vec count = 2
//      09                func 0 body size = 9
//        00              locals count = 0
//        41 00           i32.const 0          ; mem address
//        20 00           local.get 0          ; the value param
//        36 02 00        i32.store align=2 off=0
//        0b              end
//      07                func 1 body size = 7
//        00              locals count = 0
//        41 00           i32.const 0          ; mem address
//        28 02 00        i32.load align=2 off=0
//        0b              end
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

// TestWazeroRuntime_LinearMemory_IsolatedBetweenInstances is
// the headline sc-01 test for the wazero layer. We instantiate
// the same module bytecode in two separate WazeroRuntime
// instances, write a unique value into each one's linear
// memory via the module's own set() export, and assert each
// get() returns the value its own instance wrote.
//
// A pass means linear memory is per-instance, as the Wasm spec
// requires. A failure (both get()s returning the same value)
// would indicate either (a) wazero is incorrectly sharing
// backing storage between modules, or (b) NewWazeroRuntime is
// reusing a runtime / module across calls — both are
// fork-class regressions for the contract engine.
func TestWazeroRuntime_LinearMemory_IsolatedBetweenInstances(t *testing.T) {
	rtA, err := NewWazeroRuntime(isolationModuleWASM)
	if err != nil {
		t.Fatalf("NewWazeroRuntime A: %v", err)
	}
	defer rtA.Close()

	rtB, err := NewWazeroRuntime(isolationModuleWASM)
	if err != nil {
		t.Fatalf("NewWazeroRuntime B: %v", err)
	}
	defer rtB.Close()

	if _, err := rtA.Call("set", []byte("[42]")); err != nil {
		t.Fatalf("rtA.Call(set, 42): %v", err)
	}
	if _, err := rtB.Call("set", []byte("[99]")); err != nil {
		t.Fatalf("rtB.Call(set, 99): %v", err)
	}

	gotARaw, err := rtA.Call("get", nil)
	if err != nil {
		t.Fatalf("rtA.Call(get): %v", err)
	}
	gotBRaw, err := rtB.Call("get", nil)
	if err != nil {
		t.Fatalf("rtB.Call(get): %v", err)
	}

	gotA, ok := gotARaw.(uint64)
	if !ok {
		t.Fatalf("rtA get result type: got %T, want uint64", gotARaw)
	}
	gotB, ok := gotBRaw.(uint64)
	if !ok {
		t.Fatalf("rtB get result type: got %T, want uint64", gotBRaw)
	}

	if gotA != 42 {
		t.Errorf("rtA read after isolation: got %d, want 42 (rtA's own write); if equal to 99, B's memory leaked into A", gotA)
	}
	if gotB != 99 {
		t.Errorf("rtB read after isolation: got %d, want 99 (rtB's own write); if equal to 42, A's memory leaked into B", gotB)
	}

	// Re-order the writes and re-check. If memory were shared,
	// the last write would win and both reads would converge to
	// it; if isolated, each instance still sees its own newest
	// value. This second leg catches a subtle "shared but not
	// instantly observable" failure mode that the previous
	// pair-of-writes would miss.
	if _, err := rtB.Call("set", []byte("[7]")); err != nil {
		t.Fatalf("rtB.Call(set, 7): %v", err)
	}
	gotAAfter, _ := rtA.Call("get", nil)
	if v, _ := gotAAfter.(uint64); v != 42 {
		t.Errorf("rtA value after rtB.set(7): got %d, want 42 (rtA was not touched)", v)
	}
	gotBAfter, _ := rtB.Call("get", nil)
	if v, _ := gotBAfter.(uint64); v != 7 {
		t.Errorf("rtB value after rtB.set(7): got %d, want 7", v)
	}
}

// TestWazeroRuntime_RuntimeInstance_Distinct guards a structural
// invariant that backs the linear-memory isolation guarantee:
// every NewWazeroRuntime call must return a fresh runtime, not
// reuse a process-wide singleton. A regression that wired
// NewWazeroRuntime to return a cached runtime would make the
// linear-memory test above silently pass (each "instance" is
// the same instance) while still leaking state.
func TestWazeroRuntime_RuntimeInstance_Distinct(t *testing.T) {
	rtA, err := NewWazeroRuntime(isolationModuleWASM)
	if err != nil {
		t.Fatalf("NewWazeroRuntime A: %v", err)
	}
	defer rtA.Close()

	rtB, err := NewWazeroRuntime(isolationModuleWASM)
	if err != nil {
		t.Fatalf("NewWazeroRuntime B: %v", err)
	}
	defer rtB.Close()

	if rtA == rtB {
		t.Fatal("rtA and rtB are the same *WazeroRuntime; the constructor is returning a process-wide singleton, which would defeat per-contract isolation")
	}
	// Also confirm the embedded wazero.Runtime and wazero.Module
	// pointers are not aliased — these are the actual backing
	// stores for instructional memory and linear memory.
	if rtA.rt == rtB.rt {
		t.Error("rtA.rt == rtB.rt; two instances share the same wazero.Runtime, which would defeat memory isolation")
	}
	if rtA.module == rtB.module {
		t.Error("rtA.module == rtB.module; two instances share the same wazero.Module, which would alias linear memory")
	}
}
