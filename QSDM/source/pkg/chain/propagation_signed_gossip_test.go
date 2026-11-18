package chain

import (
	"testing"
	"time"
)

// 2026-11-18 report: below the signed-block activation height an unsigned block was authenticated only by its in-band
// ProducerID string, so a peer could give a P2P-only follower a forged block (copying the allowed ProducerID) or a
// forged genesis. The gossip path now refuses any block without a producer signature, at every height; history below
// the activation height comes from the HTTPS chain sync (genesis pinned).

func signedGossipTestPolicy(t *testing.T) {
	t.Helper()
	SetRequireSignedBlocks(true)
	SetSignedBlockActivationHeight(827000) // the live value: an unsigned block is still legal below it
	t.Cleanup(func() {
		SetRequireSignedBlocks(false)
		SetSignedBlockActivationHeight(0)
	})
}

func TestGossipRefusesUnsignedBlocksAtEveryHeight(t *testing.T) {
	signedGossipTestPolicy(t)
	strict := &BlockPropagator{seen: make(map[string]time.Time), requireSigned: true}
	old := &BlockPropagator{seen: make(map[string]time.Time)}

	forgedTip := makeBlock(5) // below the activation height, unsigned, any ProducerID can be copied into it
	forgedTip.ProducerID = "copied-allowlisted-producer-id"
	forgedTip.Hash = computeBlockHash(forgedTip)
	if !old.validateBlock(forgedTip) {
		t.Fatal("control: before the fix an unsigned block below the activation height passed validation")
	}
	if strict.validateBlock(forgedTip) {
		t.Fatal("an unsigned gossiped block below the activation height was accepted")
	}

	forgedGenesis := makeBlock(0)
	forgedGenesis.ProducerID = forgedTip.ProducerID
	forgedGenesis.Hash = computeBlockHash(forgedGenesis)
	if strict.validateBlock(forgedGenesis) {
		t.Fatal("an unsigned gossiped genesis was accepted")
	}

	signer, _ := newBFTKey(t)
	genuine := makeBlock(6)
	genuine.Hash = computeBlockHash(genuine)
	if err := SignBlock(genuine, signer); err != nil {
		t.Fatal(err)
	}
	if !strict.validateBlock(genuine) {
		t.Fatal("a correctly signed block was refused")
	}
}

// On the wire, through NewBlockPropagator (which sets the rule): an unsigned copy of a genuine block from an
// allowlisted producer never reaches the follower's append; the signed block does.
func TestBlockOrderE2E_UnsignedGossipBlockNeverAppended(t *testing.T) {
	signedGossipTestPolicy(t)
	blocks, producerID := signedOrderChain(t, 1)
	follower := newOrderFollower(t, producerID, blocks[:1])
	prod := newOrderProducerNode(t, blocks)
	fol := newOrderNode(t, follower.TryAppendExternalBlock)
	connectOrderNodes(t, prod, fol)

	unsigned := *blocks[1]
	unsigned.ProducerAuth = BFTWireAuth{}
	if unsigned.ProducerAuth.Signed() || unsigned.Hash != blocks[1].Hash {
		t.Fatal("test setup: the copy must be the same block without its signature")
	}
	if err := prod.bp.BroadcastBlock(&unsigned); err != nil {
		t.Fatal(err)
	}
	if !eventually(10*time.Second, func() bool { return fol.bp.Stats().Invalid >= 1 }) {
		t.Fatalf("the unsigned block was not refused as invalid: %+v", fol.bp.Stats())
	}
	if fol.calls.Load() != 0 || follower.TipHeight() != 0 {
		t.Fatalf("the unsigned block reached the append: calls %d tip %d", fol.calls.Load(), follower.TipHeight())
	}

	if err := prod.bp.BroadcastBlock(blocks[1]); err != nil {
		t.Fatal(err)
	}
	requireTip(t, follower, fol, 1)
}
