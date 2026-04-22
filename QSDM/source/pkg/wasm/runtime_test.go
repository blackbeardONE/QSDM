package wasm

import (
	"testing"
)

// Minimal valid WASM module that exports an "add" function: (i32, i32) -> i32
var minimalAddWASM = []byte{
	0x00, 0x61, 0x73, 0x6d, // magic
	0x01, 0x00, 0x00, 0x00, // version 1
	// type section: one type (i32, i32) -> (i32)
	0x01, 0x07, 0x01, 0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f,
	// function section: one function using type 0
	0x03, 0x02, 0x01, 0x00,
	// export section: export "add" as function 0
	0x07, 0x07, 0x01, 0x03, 0x61, 0x64, 0x64, 0x00, 0x00,
	// code section: function body = local.get 0 + local.get 1 + i32.add
	0x0a, 0x09, 0x01, 0x07, 0x00, 0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b,
}

func TestWazeroRuntime_BasicModule(t *testing.T) {
	rt, err := NewWazeroRuntime(minimalAddWASM)
	if err != nil {
		t.Fatalf("NewWazeroRuntime: %v", err)
	}
	defer rt.Close()

	if !rt.HasFunction("add") {
		t.Fatal("expected 'add' to be exported")
	}
	if rt.HasFunction("nonexistent") {
		t.Fatal("did not expect 'nonexistent'")
	}
}

func TestWazeroRuntime_NilCode(t *testing.T) {
	rt, err := NewWazeroRuntime(nil)
	if err != nil {
		t.Fatalf("NewWazeroRuntime(nil): %v", err)
	}
	defer rt.Close()

	_, err = rt.Call("anything", nil)
	if err == nil {
		t.Fatal("expected error calling function with no module loaded")
	}
}

func TestWazeroRuntime_CallAdd(t *testing.T) {
	rt, err := NewWazeroRuntime(minimalAddWASM)
	if err != nil {
		t.Fatalf("NewWazeroRuntime: %v", err)
	}
	defer rt.Close()

	result, err := rt.Call("add", []byte("[3, 5]"))
	if err != nil {
		t.Fatalf("Call add: %v", err)
	}

	// wazero returns uint64 for i32 results
	v, ok := result.(uint64)
	if !ok {
		t.Fatalf("expected uint64 result, got %T", result)
	}
	if v != 8 {
		t.Errorf("add(3, 5) = %d, want 8", v)
	}
}

func TestWazeroRuntime_CallValidateRaw_optionalExport(t *testing.T) {
	rt, err := NewWazeroRuntime(minimalAddWASM)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	ok, err := rt.CallValidateRaw([]byte(`{"id":"`+"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok when validate_raw is absent (pass-through)")
	}
}
