package contracts

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/wasm"
)

func TestTokenWASM_AddSub(t *testing.T) {
	rt, err := wasm.NewWazeroRuntime(tokenWASM)
	if err != nil {
		t.Fatalf("failed to load tokenWASM: %v", err)
	}
	defer rt.Close()

	if !rt.HasFunction("add") {
		t.Fatal("tokenWASM missing 'add' export")
	}
	if !rt.HasFunction("sub") {
		t.Fatal("tokenWASM missing 'sub' export")
	}

	r, err := rt.Call("add", []byte("[100, 50]"))
	if err != nil {
		t.Fatalf("add call failed: %v", err)
	}
	if r.(uint64) != 150 {
		t.Errorf("add(100, 50) = %v, want 150", r)
	}

	r, err = rt.Call("sub", []byte("[100, 30]"))
	if err != nil {
		t.Fatalf("sub call failed: %v", err)
	}
	if r.(uint64) != 70 {
		t.Errorf("sub(100, 30) = %v, want 70", r)
	}
}

func TestVotingWASM_Increment(t *testing.T) {
	rt, err := wasm.NewWazeroRuntime(votingWASM)
	if err != nil {
		t.Fatalf("failed to load votingWASM: %v", err)
	}
	defer rt.Close()

	if !rt.HasFunction("increment") {
		t.Fatal("votingWASM missing 'increment' export")
	}

	r, err := rt.Call("increment", []byte("[41]"))
	if err != nil {
		t.Fatalf("increment call failed: %v", err)
	}
	if r.(uint64) != 42 {
		t.Errorf("increment(41) = %v, want 42", r)
	}
}

func TestEscrowWASM_Clamp(t *testing.T) {
	rt, err := wasm.NewWazeroRuntime(escrowWASM)
	if err != nil {
		t.Fatalf("failed to load escrowWASM: %v", err)
	}
	defer rt.Close()

	if !rt.HasFunction("clamp") {
		t.Fatal("escrowWASM missing 'clamp' export")
	}

	tests := []struct {
		args   string
		expect uint64
		label  string
	}{
		{`[50, 10, 100]`, 50, "value in range"},
		{`[5, 10, 100]`, 10, "below min"},
		{`[200, 10, 100]`, 100, "above max"},
		{`[10, 10, 10]`, 10, "all equal"},
	}

	for _, tt := range tests {
		r, err := rt.Call("clamp", []byte(tt.args))
		if err != nil {
			t.Fatalf("clamp(%s) failed: %v", tt.label, err)
		}
		if r.(uint64) != tt.expect {
			t.Errorf("clamp(%s): got %v, want %v", tt.label, r, tt.expect)
		}
	}
}

func TestAllTemplatesHaveValidWASM(t *testing.T) {
	for _, tmpl := range GetTemplates() {
		rt, err := wasm.NewWazeroRuntime(tmpl.Code)
		if err != nil {
			t.Errorf("template %s has invalid WASM bytecode: %v", tmpl.Name, err)
			continue
		}
		rt.Close()
		t.Logf("template %s: valid WASM module (%d bytes)", tmpl.Name, len(tmpl.Code))
	}
}
