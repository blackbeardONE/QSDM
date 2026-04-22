package chain

import (
	"testing"
	"time"
)

func TestPendingProposalStore_PruneHeightsBelow(t *testing.T) {
	s := NewPendingProposalStore()
	s.Put(1, "a", &Block{Height: 1, StateRoot: "a"})
	s.Put(10, "b", &Block{Height: 10, StateRoot: "b"})
	s.PruneHeightsBelow(5)
	if _, ok := s.Get(1, "a"); ok {
		t.Fatal("height 1 should be pruned")
	}
	if _, ok := s.Get(10, "b"); !ok {
		t.Fatal("height 10 should remain")
	}
}

func TestPendingProposalStore_PutGet(t *testing.T) {
	s := NewPendingProposalStore()
	b := &Block{Height: 3, StateRoot: "sr", Timestamp: time.Unix(1, 0), ProducerID: "p"}
	b.Hash = computeBlockHash(b)
	s.Put(3, "sr", b)
	got, ok := s.Get(3, "sr")
	if !ok || got == nil || got.Hash != b.Hash {
		t.Fatalf("get %+v ok=%v", got, ok)
	}
}
