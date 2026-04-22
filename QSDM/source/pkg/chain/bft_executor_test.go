package chain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestBFTExecutor_ApplyInboundCommits(t *testing.T) {
	bc, vs := setupBFT(t)
	ex := NewBFTExecutor(bc)
	if ex == nil {
		t.Fatal("executor nil")
	}
	var published [][]byte
	ex.SetPublisher(func(b []byte) error {
		published = append(published, append([]byte(nil), b...))
		return nil
	})
	var commits int
	ex.SetOnCommitted(func(height uint64, round uint32, blockHash string) {
		commits++
		if height != 1 || round != 0 || blockHash != "hash-1" {
			t.Errorf("unexpected commit args %d %d %q", height, round, blockHash)
		}
	})

	prop, _ := bc.ProposerForRound(0)
	b, err := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{Height: 1, Round: 0, Proposer: prop, BlockHash: "hash-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ex.ApplyInbound(b); err != nil {
		t.Fatal(err)
	}
	for _, v := range vs.ActiveValidators() {
		if v.Status != ValidatorActive {
			continue
		}
		pb, _ := MarshalBFTWire(BFTWirePrevote, BFTWirePrevoteMsg{Height: 1, Round: 0, Validator: v.Address, BlockHash: "hash-1"})
		if err := ex.ApplyInbound(pb); err != nil {
			t.Fatalf("prevote %s: %v", v.Address, err)
		}
	}
	for _, v := range vs.ActiveValidators() {
		if v.Status != ValidatorActive {
			continue
		}
		cb, _ := MarshalBFTWire(BFTWirePrecommit, BFTWirePrecommitMsg{Height: 1, Round: 0, Validator: v.Address, BlockHash: "hash-1"})
		if err := ex.ApplyInbound(cb); err != nil {
			t.Fatalf("precommit %s: %v", v.Address, err)
		}
	}
	if !bc.IsCommitted(1) {
		t.Fatal("expected committed")
	}
	ex.NotifyFromConsensus(1)
	if commits != 1 {
		t.Fatalf("expected one commit callback, got %d", commits)
	}
	if len(published) == 0 {
		t.Log("publisher invoked only for explicit Broadcast*; ApplyInbound does not publish")
	}
}

func TestBFTExecutor_ApplyInboundBenignDuplicate(t *testing.T) {
	bc, _ := setupBFT(t)
	ex := NewBFTExecutor(bc)
	prop, _ := bc.ProposerForRound(0)
	b, _ := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{Height: 1, Round: 0, Proposer: prop, BlockHash: "h"})
	if err := ex.ApplyInbound(b); err != nil {
		t.Fatal(err)
	}
	if err := ex.ApplyInbound(b); err != nil {
		t.Fatalf("duplicate propose should be benign, got %v", err)
	}
}

func TestBFTExecutor_EquivocationSubmitsEvidence(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	_ = vs.Register("v3", 100)
	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	em := NewEvidenceManager(vs)
	ex := NewBFTExecutor(bc)
	ex.SetEvidenceManager(em)
	prop, _ := bc.ProposerForRound(0)
	b1, _ := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{Height: 3, Round: 0, Proposer: prop, BlockHash: "aa"})
	if err := ex.ApplyInbound(b1); err != nil {
		t.Fatal(err)
	}
	b2, _ := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{Height: 3, Round: 0, Proposer: prop, BlockHash: "bb"})
	if err := ex.ApplyInbound(b2); err == nil {
		t.Fatal("expected error")
	}
	lst := em.List()
	if len(lst) != 1 {
		t.Fatalf("expected 1 evidence record, got %d", len(lst))
	}
	if lst[0].Evidence.Type != EvidenceEquivocation {
		t.Fatalf("evidence type %v", lst[0].Evidence.Type)
	}
}

func TestBFTExecutor_ApplyInboundProposeEquivocationRejected(t *testing.T) {
	bc, _ := setupBFT(t)
	ex := NewBFTExecutor(bc)
	prop, _ := bc.ProposerForRound(0)
	b1, _ := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{Height: 2, Round: 0, Proposer: prop, BlockHash: "hash-a"})
	if err := ex.ApplyInbound(b1); err != nil {
		t.Fatal(err)
	}
	b2, _ := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{Height: 2, Round: 0, Proposer: prop, BlockHash: "hash-b"})
	err := ex.ApplyInbound(b2)
	if err == nil {
		t.Fatal("expected equivocation to surface from ApplyInbound")
	}
	if !errors.Is(err, ErrBFTEquivocation) {
		t.Fatalf("expected ErrBFTEquivocation, got %v", err)
	}
	var pe *ProposerEquivocationError
	if !errors.As(err, &pe) || pe.ExistingHash != "hash-a" || pe.NewHash != "hash-b" {
		t.Fatalf("expected structured equivocation, got %#v", err)
	}
}

func TestBFTExecutor_PendingProposeSourcePerVote(t *testing.T) {
	bc, _ := setupBFT(t)
	ex := NewBFTExecutor(bc)
	ex.SetLastInboundBFTGossipPeer("relayer-alpha")
	prop, _ := bc.ProposerForRound(0)
	sr := "state-root-99"
	blk := &Block{
		Height: 9, PrevHash: "", Timestamp: time.Unix(1700000099, 0),
		StateRoot: sr, ProducerID: "node",
	}
	blk.Hash = computeBlockHash(blk)
	b, err := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{
		Height: 9, Round: 0, Proposer: prop, BlockHash: sr, Block: blk,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ex.ApplyInbound(b); err != nil {
		t.Fatal(err)
	}
	p, ok := ex.PendingProposeSource(9, sr)
	if !ok || p != "relayer-alpha" {
		t.Fatalf("pending source: ok=%v peer=%q", ok, p)
	}
	ex.ClearPendingProposeSource(9, sr)
	if _, ok := ex.PendingProposeSource(9, sr); ok {
		t.Fatal("expected cleared pending source")
	}
	ex.SetLastInboundBFTGossipPeer("relayer-beta")
	if err := ex.ApplyInbound(b); err != nil {
		t.Fatal(err)
	}
	if p2, ok := ex.PendingProposeSource(9, sr); !ok || p2 != "relayer-beta" {
		t.Fatalf("after re-put: ok=%v peer=%q", ok, p2)
	}
	ex.PrunePendingHeight(9)
	if _, ok := ex.PendingProposeSource(9, sr); ok {
		t.Fatal("expected prune to clear pending peer map")
	}
}

func TestBFTExecutor_LastInboundBFTGossipPeer(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	ex := NewBFTExecutor(NewBFTConsensus(vs, DefaultConsensusConfig()))
	if ex.LastInboundBFTGossipPeer() != "" {
		t.Fatal("expected empty initial peer")
	}
	ex.SetLastInboundBFTGossipPeer("peer-xyz")
	if ex.LastInboundBFTGossipPeer() != "peer-xyz" {
		t.Fatalf("peer: %q", ex.LastInboundBFTGossipPeer())
	}
	ex.ClearLastInboundBFTGossipPeer()
	if ex.LastInboundBFTGossipPeer() != "" {
		t.Fatal("expected cleared")
	}
}

func TestBFTExecutor_ApplyInboundProposeWithBlockStoresPending(t *testing.T) {
	bc, _ := setupBFT(t)
	ex := NewBFTExecutor(bc)
	prop, _ := bc.ProposerForRound(0)
	sr := "root-with-body"
	blk := &Block{
		Height: 5, PrevHash: "", Timestamp: time.Unix(1700000001, 0),
		StateRoot: sr, ProducerID: "node",
	}
	blk.Hash = computeBlockHash(blk)
	b, err := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{
		Height: 5, Round: 0, Proposer: prop, BlockHash: sr, Block: blk,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ex.ApplyInbound(b); err != nil {
		t.Fatal(err)
	}
	got, ok := ex.PendingBlock(5, sr)
	if !ok || got == nil || got.Hash != blk.Hash {
		t.Fatalf("pending block missing: ok=%v", ok)
	}
}

func TestBFTExecutor_ApplyInboundProposeBadBlockHashRejected(t *testing.T) {
	bc, _ := setupBFT(t)
	ex := NewBFTExecutor(bc)
	prop, _ := bc.ProposerForRound(0)
	sr := "good-root"
	blk := &Block{
		Height: 2, PrevHash: "", Timestamp: time.Unix(1700000002, 0),
		StateRoot: "other-root", ProducerID: "node",
	}
	blk.Hash = computeBlockHash(blk)
	b, _ := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{
		Height: 2, Round: 0, Proposer: prop, BlockHash: sr, Block: blk,
	})
	if err := ex.ApplyInbound(b); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestMarshalBFTWireEnvelope(t *testing.T) {
	b, err := MarshalBFTWire(BFTWirePrevote, BFTWirePrevoteMsg{Height: 2, Round: 1, Validator: "v1", BlockHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var env BFTWireEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != BFTWirePrevote {
		t.Fatal(env.Kind)
	}
}
