package contracts

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/wasm"
)

func TestTokenV2_TransferFlow(t *testing.T) {
	rt, err := wasm.NewWazeroRuntime(tokenV2WASM)
	if err != nil {
		t.Fatalf("failed to load tokenV2WASM: %v", err)
	}
	defer rt.Close()

	for _, fn := range []string{"set_balance", "get_balance", "transfer", "add", "sub"} {
		if !rt.HasFunction(fn) {
			t.Fatalf("tokenV2WASM missing export %q", fn)
		}
	}

	// Set balance for slot 0 = 1000
	_, err = rt.Call("set_balance", []byte("[0, 1000]"))
	if err != nil {
		t.Fatalf("set_balance(0, 1000): %v", err)
	}

	// Verify balance
	r, err := rt.Call("get_balance", []byte("[0]"))
	if err != nil {
		t.Fatalf("get_balance(0): %v", err)
	}
	if r.(uint64) != 1000 {
		t.Errorf("get_balance(0) = %v, want 1000", r)
	}

	// Transfer 300 from slot 0 to slot 1
	r, err = rt.Call("transfer", []byte("[0, 1, 300]"))
	if err != nil {
		t.Fatalf("transfer(0, 1, 300): %v", err)
	}
	if r.(uint64) != 1 {
		t.Errorf("transfer should return 1 (success), got %v", r)
	}

	// Check balances after transfer
	r, _ = rt.Call("get_balance", []byte("[0]"))
	if r.(uint64) != 700 {
		t.Errorf("sender balance = %v, want 700", r)
	}
	r, _ = rt.Call("get_balance", []byte("[1]"))
	if r.(uint64) != 300 {
		t.Errorf("recipient balance = %v, want 300", r)
	}

	// Attempt transfer with insufficient funds
	r, err = rt.Call("transfer", []byte("[0, 1, 9999]"))
	if err != nil {
		t.Fatalf("transfer should not error: %v", err)
	}
	if r.(uint64) != 0 {
		t.Errorf("transfer with insufficient funds should return 0, got %v", r)
	}

	// Balances unchanged after failed transfer
	r, _ = rt.Call("get_balance", []byte("[0]"))
	if r.(uint64) != 700 {
		t.Errorf("sender balance should be unchanged at 700, got %v", r)
	}
}

func TestTokenV2_V1Compat(t *testing.T) {
	rt, err := wasm.NewWazeroRuntime(tokenV2WASM)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer rt.Close()

	r, _ := rt.Call("add", []byte("[100, 50]"))
	if r.(uint64) != 150 {
		t.Errorf("add(100,50) = %v, want 150", r)
	}

	r, _ = rt.Call("sub", []byte("[100, 30]"))
	if r.(uint64) != 70 {
		t.Errorf("sub(100,30) = %v, want 70", r)
	}
}

func TestVotingV2_VoteFlow(t *testing.T) {
	rt, err := wasm.NewWazeroRuntime(votingV2WASM)
	if err != nil {
		t.Fatalf("failed to load votingV2WASM: %v", err)
	}
	defer rt.Close()

	for _, fn := range []string{"vote_yes", "vote_no", "get_yes", "get_no", "increment"} {
		if !rt.HasFunction(fn) {
			t.Fatalf("votingV2WASM missing export %q", fn)
		}
	}

	// Vote yes 3 times on proposal slot 0
	for i := 0; i < 3; i++ {
		r, err := rt.Call("vote_yes", []byte("[0]"))
		if err != nil {
			t.Fatalf("vote_yes: %v", err)
		}
		if r.(uint64) != uint64(i+1) {
			t.Errorf("vote_yes iteration %d: got %v, want %d", i, r, i+1)
		}
	}

	// Vote no twice
	for i := 0; i < 2; i++ {
		rt.Call("vote_no", []byte("[0]"))
	}

	// Check tallies
	r, _ := rt.Call("get_yes", []byte("[0]"))
	if r.(uint64) != 3 {
		t.Errorf("get_yes = %v, want 3", r)
	}
	r, _ = rt.Call("get_no", []byte("[0]"))
	if r.(uint64) != 2 {
		t.Errorf("get_no = %v, want 2", r)
	}

	// Different proposal (slot 1) should be independent
	r, _ = rt.Call("get_yes", []byte("[1]"))
	if r.(uint64) != 0 {
		t.Errorf("slot 1 yes = %v, want 0", r)
	}
}

func TestVotingV2_V1Compat(t *testing.T) {
	rt, err := wasm.NewWazeroRuntime(votingV2WASM)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer rt.Close()

	r, _ := rt.Call("increment", []byte("[41]"))
	if r.(uint64) != 42 {
		t.Errorf("increment(41) = %v, want 42", r)
	}
}

func TestEscrowV2_DepositReleaseRefund(t *testing.T) {
	rt, err := wasm.NewWazeroRuntime(escrowV2WASM)
	if err != nil {
		t.Fatalf("failed to load escrowV2WASM: %v", err)
	}
	defer rt.Close()

	for _, fn := range []string{"deposit", "get_amount", "get_status", "release", "refund", "clamp"} {
		if !rt.HasFunction(fn) {
			t.Fatalf("escrowV2WASM missing export %q", fn)
		}
	}

	// Deposit 500 to slot 0
	r, err := rt.Call("deposit", []byte("[0, 500]"))
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}
	if r.(uint64) != 0 {
		t.Errorf("deposit should return slot 0, got %v", r)
	}

	// Check amount and status
	r, _ = rt.Call("get_amount", []byte("[0]"))
	if r.(uint64) != 500 {
		t.Errorf("amount = %v, want 500", r)
	}
	r, _ = rt.Call("get_status", []byte("[0]"))
	if r.(uint64) != 1 {
		t.Errorf("status = %v, want 1 (held)", r)
	}

	// Release slot 0
	r, err = rt.Call("release", []byte("[0]"))
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if r.(uint64) != 1 {
		t.Errorf("release should return 1 (success), got %v", r)
	}

	// Status should now be 2 (released)
	r, _ = rt.Call("get_status", []byte("[0]"))
	if r.(uint64) != 2 {
		t.Errorf("status after release = %v, want 2", r)
	}

	// Can't release again
	r, _ = rt.Call("release", []byte("[0]"))
	if r.(uint64) != 0 {
		t.Errorf("double release should return 0, got %v", r)
	}

	// Deposit to slot 1 and refund
	rt.Call("deposit", []byte("[1, 250]"))
	r, _ = rt.Call("refund", []byte("[1]"))
	if r.(uint64) != 1 {
		t.Errorf("refund should return 1, got %v", r)
	}
	r, _ = rt.Call("get_status", []byte("[1]"))
	if r.(uint64) != 3 {
		t.Errorf("status after refund = %v, want 3", r)
	}
}

func TestEscrowV2_V1Compat(t *testing.T) {
	rt, err := wasm.NewWazeroRuntime(escrowV2WASM)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer rt.Close()

	r, _ := rt.Call("clamp", []byte("[5, 10, 100]"))
	if r.(uint64) != 10 {
		t.Errorf("clamp(5,10,100) = %v, want 10", r)
	}
}
