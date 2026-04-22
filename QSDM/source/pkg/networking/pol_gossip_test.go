package networking

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestPolGossipIngress_ValidPrevoteLock(t *testing.T) {
	ing := NewPolGossipIngress(DefaultPolGossipConfig(), nil)
	p := &chain.PrevoteLockProof{
		Height:          2,
		Round:           0,
		LockedBlockHash: "h1",
		Prevotes:        []chain.BlockVote{{Validator: "v1", BlockHash: "h1", Height: 2, Round: 0, Type: chain.VotePreVote}},
	}
	inner, err := chain.EncodePrevoteLockProof(p)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(polGossipWire{Kind: polKindPrevoteLock, Payload: inner})
	if err != nil {
		t.Fatal(err)
	}
	if err := ing.HandlePeerMessage("peer1", body); err != nil {
		t.Fatal(err)
	}
	if err := ing.HandlePeerMessage("peer1", body); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestPolGossipIngress_ValidRoundCertificate(t *testing.T) {
	ing := NewPolGossipIngress(DefaultPolGossipConfig(), nil)
	cert := chain.RoundCertificate{
		Height:       5,
		Round:        0,
		Proposer:     "p",
		BlockHash:    "b",
		CommitDigest: "deadbeef",
		ValidatorSet: []string{"a"},
		CommitCount:    1,
		NilCommitCount: 0,
	}
	inner, err := json.Marshal(cert)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(polGossipWire{Kind: polKindRoundCertificate, Payload: inner})
	if err != nil {
		t.Fatal(err)
	}
	if err := ing.HandlePeerMessage("peer2", body); err != nil {
		t.Fatal(err)
	}
}

func TestPolGossipIngress_RateLimit(t *testing.T) {
	cfg := DefaultPolGossipConfig()
	cfg.MaxPerWindow = 2
	cfg.RateWindow = time.Hour
	ing := NewPolGossipIngress(cfg, nil)
	makeBody := func(h uint64) []byte {
		p := &chain.PrevoteLockProof{Height: h, Round: 0, LockedBlockHash: "x"}
		inner, _ := chain.EncodePrevoteLockProof(p)
		b, _ := json.Marshal(polGossipWire{Kind: polKindPrevoteLock, Payload: inner})
		return b
	}
	if err := ing.HandlePeerMessage("p", makeBody(10)); err != nil {
		t.Fatal(err)
	}
	if err := ing.HandlePeerMessage("p", makeBody(11)); err != nil {
		t.Fatal(err)
	}
	if err := ing.HandlePeerMessage("p", makeBody(12)); err == nil {
		t.Fatal("expected rate limit")
	}
}

func TestPolGossipIngress_FollowerRejectsBadQuorum(t *testing.T) {
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	f := chain.NewPolFollower(vs, 2.0/3.0)
	ing := NewPolGossipIngress(DefaultPolGossipConfig(), f)
	p := &chain.PrevoteLockProof{
		Height:          2,
		Round:           0,
		LockedBlockHash: "h1",
		Prevotes:        []chain.BlockVote{{Validator: "v1", BlockHash: "h1", Height: 2, Round: 0, Type: chain.VotePreVote}},
	}
	inner, _ := chain.EncodePrevoteLockProof(p)
	body, _ := json.Marshal(polGossipWire{Kind: polKindPrevoteLock, Payload: inner})
	if err := ing.HandlePeerMessage("peer1", body); err == nil {
		t.Fatal("expected follower quorum rejection")
	}
}

func TestPolGossipIngress_FollowerAcceptsQuorumProof(t *testing.T) {
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	f := chain.NewPolFollower(vs, 2.0/3.0)
	ing := NewPolGossipIngress(DefaultPolGossipConfig(), f)
	p := &chain.PrevoteLockProof{
		Height:          2,
		Round:           0,
		LockedBlockHash: "h1",
		Prevotes: []chain.BlockVote{
			{Validator: "v1", BlockHash: "h1", Height: 2, Round: 0, Type: chain.VotePreVote, Timestamp: time.Now()},
			{Validator: "v2", BlockHash: "h1", Height: 2, Round: 0, Type: chain.VotePreVote, Timestamp: time.Now()},
		},
	}
	inner, _ := chain.EncodePrevoteLockProof(p)
	body, _ := json.Marshal(polGossipWire{Kind: polKindPrevoteLock, Payload: inner})
	if err := ing.HandlePeerMessage("peer1", body); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.GetPrevoteLockProof(2); !ok {
		t.Fatal("expected follower to store proof")
	}
}
