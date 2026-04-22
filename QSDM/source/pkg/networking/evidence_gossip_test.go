package networking

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestEvidenceGossipIngress_AcceptAndDedupe(t *testing.T) {
	em, _ := chainEvidenceManager(t)
	rep := NewReputationTracker(DefaultReputationConfig())
	cfg := DefaultEvidenceGossipConfig()
	cfg.MaxPerWindow = 100
	eg := NewEvidenceGossipIngress(em, rep, cfg)

	ev := chain.ConsensusEvidence{
		Type:        chain.EvidenceInvalidVote,
		Validator:   "v1",
		Height:      1,
		Round:       0,
		Details:     "bad vote",
		Timestamp:   time.Now(),
	}
	payload, _ := json.Marshal(ev)

	if err := eg.HandlePeerMessage("peer-a", payload); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if err := eg.HandlePeerMessage("peer-a", payload); err == nil {
		t.Fatal("expected duplicate gossip error")
	}
}

func TestEvidenceGossipIngress_RateLimit(t *testing.T) {
	em, _ := chainEvidenceManager(t)
	rep := NewReputationTracker(DefaultReputationConfig())
	cfg := DefaultEvidenceGossipConfig()
	cfg.MaxPerWindow = 2
	cfg.RateWindow = time.Minute
	eg := NewEvidenceGossipIngress(em, rep, cfg)

	for i := 0; i < 2; i++ {
		ev := chain.ConsensusEvidence{
			Type:      chain.EvidenceInvalidVote,
			Validator: "v1",
			Height:    uint64(100 + i),
			Round:     0,
			Details:   "spam",
			Timestamp: time.Now(),
		}
		b, _ := json.Marshal(ev)
		if err := eg.HandlePeerMessage("spammer", b); err != nil {
			t.Fatalf("msg %d: %v", i, err)
		}
	}
	ev3 := chain.ConsensusEvidence{
		Type:      chain.EvidenceInvalidVote,
		Validator: "v1",
		Height:    103,
		Round:     0,
		Details:   "spam",
		Timestamp: time.Now(),
	}
	b3, _ := json.Marshal(ev3)
	if err := eg.HandlePeerMessage("spammer", b3); err == nil {
		t.Fatal("expected rate limit error")
	}
}

func chainEvidenceManager(t *testing.T) (*chain.EvidenceManager, *chain.ValidatorSet) {
	t.Helper()
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	if err := vs.Register("v1", 500); err != nil {
		t.Fatal(err)
	}
	return chain.NewEvidenceManager(vs), vs
}
