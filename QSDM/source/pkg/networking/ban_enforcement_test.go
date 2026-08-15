package networking

import (
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
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

// The tests above drive HandlePeerMessage, which has ZERO production callers.
// The live pubsub loop calls TryConsumeGossip (libp2p.go:258), which carried
// the same dispatch without the ban check -- so every one of those tests passed
// while bans were unenforced on the only path that runs. Testing the function
// that was never broken is how a guard ships with its own defect intact; this
// file already had that shape and did not catch it.
func TestTryConsumeGossip_dropsBannedPeersTransaction(t *testing.T) {
	as := chain.NewAccountStore()
	as.Credit("alice", 100)
	rt := banTracker(t, "bad-peer")
	// Fully wired on purpose. With a nil validator this test still passes -- the
	// ban returns before the validator is touched -- but NEUTERING the ban then
	// fails it with a nil-pointer panic rather than the assertion. A guard whose
	// failure mode is a panic is not demonstrating the property it names.
	ti := NewTxGossipIngress(
		chain.NewGossipValidator(chain.NewSigVerifier(), chain.NewTxValidator(as), chain.DefaultGossipValidationConfig()),
		mempool.New(mempool.DefaultConfig()),
		rt,
	)

	// True means "consumed here, do not pass to the legacy byte handler".
	// For a banned peer that IS the drop: returning false would hand the
	// payload to a handler with no ban check at all.
	if !ti.TryConsumeGossip("bad-peer", []byte(`{"tx":{"id":"x","sender":"alice"}}`)) {
		t.Fatal("a banned peer's transaction must be consumed and dropped, not " +
			"passed through to the legacy handler")
	}
}

// The ban must not reach traffic it was never scored on. TryConsumeGossip sees
// every pubsub topic, so refusing before the decode -- or delegating to
// HandlePeerMessage, which penalises anything that fails to decode as a tx --
// would make a tx-reputation ban swallow block and BFT gossip, and would record
// EventInvalidTx against honest peers for every block they relay.
func TestTryConsumeGossip_leavesNonTransactionGossipAlone(t *testing.T) {
	rt := banTracker(t, "bad-peer")
	ti := NewTxGossipIngress(nil, nil, rt)

	// Shaped like a block/BFT payload: valid JSON, not a signed tx or envelope.
	blockish := []byte(`{"height":42,"prev_hash":"abc"}`)
	if ti.TryConsumeGossip("bad-peer", blockish) {
		t.Error("non-transaction gossip must fall through to the legacy handler " +
			"even from a banned peer; consuming it widens a tx ban to block relay")
	}

	// And an honest peer must not be penalised for that same message.
	before := rt.IsBanned("good-peer")
	if ti.TryConsumeGossip("good-peer", blockish) {
		t.Error("non-transaction gossip from an honest peer must fall through")
	}
	if !before && rt.IsBanned("good-peer") {
		t.Error("relaying a non-transaction message must not count as an invalid tx")
	}
}

// The honest path must still work, or the guard above is satisfied by dropping
// everything -- which would look identical in the banned-peer test.
//
// This needs a fully-wired ingress. The first version passed nil for the
// validator and mempool, copying the banned-peer tests above, and panicked on a
// nil *GossipValidator -- because those tests never reach the validator: the ban
// short-circuits first. A fixture that only works when the code under test
// returns early is not exercising the path it claims to.
func TestTryConsumeGossip_stillProcessesUnbannedPeers(t *testing.T) {
	as := chain.NewAccountStore()
	as.Credit("alice", 100)
	rt := NewReputationTracker(DefaultReputationConfig())
	ti := NewTxGossipIngress(
		chain.NewGossipValidator(chain.NewSigVerifier(), chain.NewTxValidator(as), chain.DefaultGossipValidationConfig()),
		mempool.New(mempool.DefaultConfig()),
		rt,
	)

	// An unsigned tx from an unbanned peer must REACH the validator and be judged
	// on its merits. The assertion is that it is not short-circuited by a ban that
	// does not apply -- reaching the validator at all is the property under test.
	ti.TryConsumeGossip("good-peer", []byte(`{"tx":{"id":"x","sender":"alice"}}`))
	if rt.IsBanned("good-peer") {
		t.Error("a single unadmitted transaction must not ban an honest peer")
	}
}
