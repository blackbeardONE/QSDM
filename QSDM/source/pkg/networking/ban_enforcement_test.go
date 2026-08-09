package networking

import (
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// banTracker returns a tracker with peerID already driven past the ban
// threshold via the same event the production ingresses record.
func banTracker(t *testing.T, peerID string) *ReputationTracker {
	t.Helper()
	rt := NewReputationTracker(DefaultReputationConfig())
	for i := 0; i < 10 && !rt.IsBanned(peerID); i++ {
		rt.RecordEvent(peerID, EventInvalidBlock, 0)
	}
	if !rt.IsBanned(peerID) {
		t.Fatalf("precondition: peer %s should be banned", peerID)
	}
	return rt
}

// TestTxGossipIngress_refusesBannedPeer is the regression test for bans that
// were recorded but never enforced: RecordEvent was called throughout the
// ingress paths while IsBanned had zero production consultations, so banning
// a peer changed nothing about how its messages were handled.
func TestTxGossipIngress_refusesBannedPeer(t *testing.T) {
	rt := banTracker(t, "bad-peer")
	ti := NewTxGossipIngress(nil, nil, rt)

	verdict, err := ti.HandlePeerMessage("bad-peer", []byte(`{"tx":{"id":"x"}}`))
	if err == nil {
		t.Fatal("banned peer's gossip must be refused")
	}
	if !strings.Contains(err.Error(), "banned") {
		t.Fatalf("expected a ban refusal, got %v", err)
	}
	if verdict != chain.GossipRejected {
		t.Fatalf("expected rejected verdict, got %q", verdict)
	}
}

func TestEvidenceGossipIngress_refusesBannedPeer(t *testing.T) {
	rt := banTracker(t, "bad-peer")
	eg := NewEvidenceGossipIngress(nil, rt, EvidenceGossipConfig{})

	err := eg.HandlePeerMessage("bad-peer", []byte(`{}`))
	if err == nil {
		t.Fatal("banned peer's evidence must be refused")
	}
	if !strings.Contains(err.Error(), "banned") {
		t.Fatalf("expected a ban refusal, got %v", err)
	}
}

func TestBFTGossipIngress_refusesBannedPeer(t *testing.T) {
	rt := banTracker(t, "bad-peer")
	g := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
	g.SetReputationTracker(rt)

	err := g.HandlePeerMessage("bad-peer", []byte(`{"kind":"prevote","payload":"e30="}`))
	if err == nil {
		t.Fatal("banned peer's BFT gossip must be refused")
	}
	if !strings.Contains(err.Error(), "banned") {
		t.Fatalf("expected a ban refusal, got %v", err)
	}
}

// An unbanned peer must still be processed normally — the gate must key on
// ban state, not simply reject everything once a tracker is attached.
func TestGossipIngress_allowsUnbannedPeer(t *testing.T) {
	rt := banTracker(t, "bad-peer")
	eg := NewEvidenceGossipIngress(nil, rt, EvidenceGossipConfig{})

	err := eg.HandlePeerMessage("good-peer", []byte(`not-json`))
	if err == nil {
		t.Fatal("expected a decode error for the malformed payload")
	}
	if strings.Contains(err.Error(), "banned") {
		t.Fatalf("unbanned peer must not be refused as banned: %v", err)
	}
}

// TestBFTGossipIngress_participationGate covers the change from a static
// boot-time catch-up decision to one that follows derived membership.
//
// cmd/qsdm used to nil the executor whenever QSDM_NETWORKED_CATCHUP_MODE was
// set, so a node started as a replica stayed one for the life of the
// process — even after it bonded stake and legitimately joined the validator
// set. Participation now follows membership, which is reconciled on every
// commit, so a home node that bonds starts participating without a restart.
func TestBFTGossipIngress_participationGate(t *testing.T) {
	participating := false
	g := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
	g.SetParticipationGate(func() bool { return participating })

	if g.participating() {
		t.Fatal("gate should report non-participating while the predicate is false")
	}

	participating = true
	if !g.participating() {
		t.Fatal("gate must follow the predicate without reconstruction")
	}
}

// A nil gate means "participate", preserving full-validator behaviour for
// callers that never set one.
func TestBFTGossipIngress_nilGateParticipates(t *testing.T) {
	g := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
	if !g.participating() {
		t.Fatal("a nil participation gate must default to participating")
	}
}

// Validation, dedupe and relay must run regardless of participation, so a
// replica still helps propagate consensus traffic.
func TestBFTGossipIngress_nonParticipantStillValidates(t *testing.T) {
	g := NewBFTGossipIngress(DefaultBFTGossipConfig(), nil)
	g.SetParticipationGate(func() bool { return false })

	// Malformed payload must still be rejected at the wire layer.
	if err := g.HandlePeerMessage("peer", []byte("not-json")); err == nil {
		t.Fatal("a non-participating node must still validate wire framing")
	}
	if g.Stats().RejectedWire == 0 {
		t.Fatal("wire rejection should be counted even when not participating")
	}

	// A well-formed message is accepted (validated + deduped) without error.
	good := []byte(`{"kind":"bft_prevote","payload":"e30="}`)
	if err := g.HandlePeerMessage("peer", good); err != nil {
		t.Fatalf("well-formed gossip should pass validation on a replica: %v", err)
	}
	// And the duplicate is dropped, proving dedupe ran.
	if err := g.HandlePeerMessage("peer", good); err == nil {
		t.Fatal("dedupe must run on a non-participating node too")
	}
}
