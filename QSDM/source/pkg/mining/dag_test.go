package mining

import (
	"testing"
)

func TestInMemoryDAGSeedAndChain(t *testing.T) {
	var root [32]byte
	root[0] = 0xAB
	d, err := NewInMemoryDAG(7, root, 16)
	if err != nil {
		t.Fatalf("build dag: %v", err)
	}
	if d.N() != 16 {
		t.Fatalf("N = %d want 16", d.N())
	}
	got0, err := d.Get(0)
	if err != nil {
		t.Fatalf("get 0: %v", err)
	}
	want0 := DAGSeed(7, root)
	if got0 != want0 {
		t.Fatalf("D[0] mismatch: got %x want %x", got0[:], want0[:])
	}
	// Spot-check a later entry matches what the lazy implementation says.
	lazy, err := NewLazyDAG(7, root, 16)
	if err != nil {
		t.Fatalf("lazy build: %v", err)
	}
	for i := uint32(0); i < 16; i++ {
		a, _ := d.Get(i)
		b, _ := lazy.Get(i)
		if a != b {
			t.Fatalf("in-memory vs lazy differ at idx %d: %x vs %x", i, a[:], b[:])
		}
	}
}

func TestInMemoryDAGRejectsTinyN(t *testing.T) {
	if _, err := NewInMemoryDAG(0, [32]byte{}, 1); err == nil {
		t.Fatal("N=1 must be rejected (need >= 2)")
	}
}

func TestInMemoryDAGOutOfRange(t *testing.T) {
	d, _ := NewInMemoryDAG(0, [32]byte{}, 4)
	if _, err := d.Get(4); err == nil {
		t.Fatal("out-of-range Get must error")
	}
}

func TestLazyDAGMatchesInMemory(t *testing.T) {
	var root [32]byte
	root[31] = 0xCD
	const N = 32
	mem, _ := NewInMemoryDAG(42, root, N)
	lazy, _ := NewLazyDAG(42, root, N)
	for i := uint32(0); i < N; i++ {
		a, _ := mem.Get(i)
		b, _ := lazy.Get(i)
		if a != b {
			t.Fatalf("divergence at %d: mem=%x lazy=%x", i, a[:], b[:])
		}
	}
}
