package chain

import (
	"errors"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// externalAuthzFixture builds a producer plus a well-formed genesis block that
// would be accepted on every guard except authorization. The block is
// deliberately unsigned, which is the deployed posture: live blocks on
// api.qsdm.tech carry no producer_auth, so VerifyBlockSignature returns nil and
// authorization is the only thing that can reject the sender.
func externalAuthzFixture(t *testing.T, producerID string) (*BlockProducer, *Block) {
	t.Helper()
	as := NewAccountStore()
	as.Credit("alice", 100)
	bp := NewBlockProducer(mempool.New(mempool.DefaultConfig()), as, DefaultProducerConfig())
	blk := &Block{
		Height:     0,
		PrevHash:   "",
		Timestamp:  time.Unix(1700000100, 0),
		StateRoot:  as.StateRoot(),
		ProducerID: producerID,
	}
	blk.Hash = computeBlockHash(blk)
	return bp, blk
}

func TestExternalAuthz_EmptyAllowlistAcceptsAnyProducer(t *testing.T) {
	// The default must be byte-identical to the pre-existing behaviour: a node
	// that does not configure the gate keeps following the chain.
	bp, blk := externalAuthzFixture(t, "some-unknown-peer")
	if bp.ExternalProducerGateEnforced() {
		t.Fatal("gate must be open until producers are pinned")
	}
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatalf("unconfigured allowlist must accept: %v", err)
	}
	if h := bp.ChainHeight(); h != 0 {
		t.Fatalf("expected the block to be appended, tip height %d", h)
	}
}

func TestExternalAuthz_RejectsUnauthorizedProducer(t *testing.T) {
	// The vulnerability: an arbitrary peer builds a block on our tip and every
	// honest node replays its transactions. With a pinned producer it is
	// refused before any replay happens.
	bp, blk := externalAuthzFixture(t, "attacker-peer")
	bp.SetAuthorizedBlockProducers([]string{"12D3KooWRH4MGiaRYMZEr9LvdxYrpePT5LPbNqLTMGukD32yhkZ8"})

	err := bp.TryAppendExternalBlock(blk)
	if err == nil {
		t.Fatal("expected an unauthorized producer to be rejected")
	}
	if !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("expected ErrExternalProducerNotAuthorized, got %v", err)
	}
	if h := bp.ChainHeight(); h != 0 || len(bp.chain) != 0 {
		t.Fatalf("rejected block must not be appended: chain len %d", len(bp.chain))
	}
}

func TestExternalAuthz_AcceptsAuthorizedProducer(t *testing.T) {
	const producer = "12D3KooWRH4MGiaRYMZEr9LvdxYrpePT5LPbNqLTMGukD32yhkZ8"
	bp, blk := externalAuthzFixture(t, producer)
	bp.SetAuthorizedBlockProducers([]string{producer})

	if !bp.ExternalProducerGateEnforced() {
		t.Fatal("gate should report enforced once a producer is pinned")
	}
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatalf("authorized producer must be accepted: %v", err)
	}
	if h := bp.ChainHeight(); h != 0 {
		t.Fatalf("expected tip height 0, got %d", h)
	}
}

func TestExternalAuthz_BlankEntriesDoNotAuthorizeEmptyProducer(t *testing.T) {
	// A stray comma in configuration yields an empty entry. If that were kept,
	// it would authorize a block whose ProducerID is absent -- exactly the
	// shape an attacker would send to slip through a sloppy allowlist.
	bp, blk := externalAuthzFixture(t, "")
	bp.SetAuthorizedBlockProducers([]string{"  ", "", "trusted-peer"})

	if got := bp.AuthorizedBlockProducers(); len(got) != 1 || got[0] != "trusted-peer" {
		t.Fatalf("blank entries must be dropped, got %v", got)
	}
	if err := bp.TryAppendExternalBlock(blk); !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("empty producer id must not be authorized, got %v", err)
	}
}

func TestExternalAuthz_AllEntriesBlankLeavesGateOpen(t *testing.T) {
	// Dropping blanks must not turn a whitespace-only setting into a
	// fail-closed gate that silently stops the node following the chain.
	bp, blk := externalAuthzFixture(t, "any-peer")
	bp.SetAuthorizedBlockProducers([]string{" ", ""})

	if bp.ExternalProducerGateEnforced() {
		t.Fatal("an all-blank allowlist must leave the gate open")
	}
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatalf("expected open gate to accept, got %v", err)
	}
}

func TestExternalAuthz_ClearingRestoresOpenGate(t *testing.T) {
	bp, blk := externalAuthzFixture(t, "any-peer")
	bp.SetAuthorizedBlockProducers([]string{"trusted-peer"})
	if err := bp.TryAppendExternalBlock(blk); !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("precondition: expected rejection, got %v", err)
	}
	bp.SetAuthorizedBlockProducers(nil)
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatalf("clearing the allowlist must restore the open gate: %v", err)
	}
}

func TestExternalAuthz_RejectionPrecedesReplay(t *testing.T) {
	// Authorization is checked before the clone-and-replay, so an unauthorized
	// peer cannot make us spend a full state replay, and a block that is
	// unauthorized AND malformed reports the authorization failure rather than
	// leaking which other guard it would also have tripped.
	bp, blk := externalAuthzFixture(t, "attacker-peer")
	blk.StateRoot = "definitely-not-the-replayed-root"
	blk.Hash = computeBlockHash(blk)
	bp.SetAuthorizedBlockProducers([]string{"trusted-peer"})

	if err := bp.TryAppendExternalBlock(blk); !errors.Is(err, ErrExternalProducerNotAuthorized) {
		t.Fatalf("authorization must be reported first, got %v", err)
	}
}
