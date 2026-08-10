package networking

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestBFTGossipIngress_InvalidPayload(t *testing.T) {
	g := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
	if err := g.HandlePeerMessage("p1", []byte(`{}`)); err == nil {
		t.Fatal("expected error")
	}
}

func TestBFTGossipIngress_ReceiveOnlyStillAuthenticates(t *testing.T) {
	validators := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	authExec := chain.NewBFTExecutor(chain.NewBFTConsensus(validators, chain.DefaultConsensusConfig()))
	authExec.SetRequireSignedVotes(true)
	authExec.SetSignedVoteActivationHeight(10)
	ingress := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
	ingress.SetAuthenticationExecutor(authExec)

	unsigned, err := chain.MarshalBFTWire(chain.BFTWirePrevote, chain.BFTWirePrevoteMsg{
		Height: 10, Validator: "forged", BlockHash: "root",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ingress.HandlePeerMessage("peer", unsigned); !errors.Is(err, chain.ErrBFTUnsigned) {
		t.Fatalf("receive-only ingress must reject unsigned votes at activation, got %v", err)
	}

	signer, _, err := chain.LoadOrCreateBFTSigner(filepath.Join(t.TempDir(), "consensus.json"))
	if err != nil {
		t.Fatal(err)
	}
	msg := chain.BFTWirePrevoteMsg{Height: 11, Validator: signer.Address(), BlockHash: "root"}
	if err := chain.SignPrevote(&msg, signer); err != nil {
		t.Fatal(err)
	}
	msg.Auth.Signature[0] ^= 0xff
	badSignature, err := chain.MarshalBFTWire(chain.BFTWirePrevote, msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := ingress.HandlePeerMessage("peer", badSignature); !errors.Is(err, chain.ErrBFTBadSignature) {
		t.Fatalf("receive-only ingress must reject invalid signatures, got %v", err)
	}
}

func TestBFTGossipIngress_StatsDedupe(t *testing.T) {
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	for _, reg := range []struct{ addr string; stake float64 }{
		{"v1", 100}, {"v2", 100}, {"v3", 100},
	} {
		if err := vs.Register(reg.addr, reg.stake); err != nil {
			t.Fatal(err)
		}
	}
	bc := chain.NewBFTConsensus(vs, chain.DefaultConsensusConfig())
	ex := chain.NewBFTExecutor(bc)
	g := NewBFTGossipIngress(DefaultBFTGossipConfig(), ex)
	prop, _ := bc.ProposerForRound(0)
	b, _ := chain.MarshalBFTWire(chain.BFTWirePropose, chain.BFTWireProposeMsg{Height: 5, Round: 0, Proposer: prop, BlockHash: "x"})
	if err := g.HandlePeerMessage("p1", b); err != nil {
		t.Fatal(err)
	}
	if err := g.HandlePeerMessage("p1", b); err == nil {
		t.Fatal("expected duplicate error")
	}
	st := g.Stats()
	if st.IngressOK != 1 || st.DedupeDropped != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestBFTGossipIngress_Apply(t *testing.T) {
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	for _, reg := range []struct{ addr string; stake float64 }{
		{"v1", 100}, {"v2", 100}, {"v3", 100},
	} {
		if err := vs.Register(reg.addr, reg.stake); err != nil {
			t.Fatal(err)
		}
	}
	bc := chain.NewBFTConsensus(vs, chain.DefaultConsensusConfig())
	ex := chain.NewBFTExecutor(bc)
	g := NewBFTGossipIngress(DefaultBFTGossipConfig(), ex)

	prop, _ := bc.ProposerForRound(0)
	b, _ := chain.MarshalBFTWire(chain.BFTWirePropose, chain.BFTWireProposeMsg{Height: 10, Round: 0, Proposer: prop, BlockHash: "sr"})
	if err := g.HandlePeerMessage("peerA", b); err != nil {
		t.Fatal(err)
	}
	for _, v := range vs.ActiveValidators() {
		pb, _ := chain.MarshalBFTWire(chain.BFTWirePrevote, chain.BFTWirePrevoteMsg{Height: 10, Round: 0, Validator: v.Address, BlockHash: "sr"})
		if err := g.HandlePeerMessage("peerA", pb); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range vs.ActiveValidators() {
		cb, _ := chain.MarshalBFTWire(chain.BFTWirePrecommit, chain.BFTWirePrecommitMsg{Height: 10, Round: 0, Validator: v.Address, BlockHash: "sr"})
		if err := g.HandlePeerMessage("peerA", cb); err != nil {
			t.Fatal(err)
		}
	}
	if !bc.IsCommitted(10) {
		t.Fatal("expected height 10 committed via gossip ingress")
	}
	if ex.LastInboundBFTGossipPeer() != "peerA" {
		t.Fatalf("expected last BFT gossip peer peerA, got %q", ex.LastInboundBFTGossipPeer())
	}
}

func TestBFTGossipIngress_EquivocationReputation(t *testing.T) {
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	for _, reg := range []struct{ addr string; stake float64 }{
		{"v1", 100}, {"v2", 100}, {"v3", 100},
	} {
		if err := vs.Register(reg.addr, reg.stake); err != nil {
			t.Fatal(err)
		}
	}
	bc := chain.NewBFTConsensus(vs, chain.DefaultConsensusConfig())
	ex := chain.NewBFTExecutor(bc)
	rep := NewReputationTracker(DefaultReputationConfig())
	g := NewBFTGossipIngress(DefaultBFTGossipConfig(), ex)
	g.SetReputationTracker(rep)

	prop, _ := bc.ProposerForRound(0)
	b1, _ := chain.MarshalBFTWire(chain.BFTWirePropose, chain.BFTWireProposeMsg{Height: 7, Round: 0, Proposer: prop, BlockHash: "first"})
	if err := g.HandlePeerMessage("peerX", b1); err != nil {
		t.Fatal(err)
	}
	b2, _ := chain.MarshalBFTWire(chain.BFTWirePropose, chain.BFTWireProposeMsg{Height: 7, Round: 0, Proposer: prop, BlockHash: "second"})
	err := g.HandlePeerMessage("peerX", b2)
	if err == nil || !errors.Is(err, chain.ErrBFTEquivocation) {
		t.Fatalf("expected ErrBFTEquivocation, got %v", err)
	}
	rec, ok := rep.GetPeer("peerX")
	if !ok {
		t.Fatal("expected peer record")
	}
	if rec.Violations == 0 {
		t.Fatal("expected protocol violation on equivocation relay")
	}
}
