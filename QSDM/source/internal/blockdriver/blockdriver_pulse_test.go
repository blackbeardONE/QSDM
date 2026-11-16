package blockdriver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// PULSE: with a PulsePayee, a member's reward is addressed to the account the
// hook names (its escrow account), carries the member's own proof IDs, and
// its reward ID is derived from that recipient. Without the hook nothing
// changes (the other tests).
func TestTick_PulsePayeeReroutesOnlyMembers(t *testing.T) {
	alice := "aa" + fmt.Sprintf("%062x", 1)
	bob := "bb" + fmt.Sprintf("%062x", 2)
	pool := "cc" + fmt.Sprintf("%062x", 3)
	escrow := chain.PulseEscrowAddress(pool, alice)
	var askedHeight uint64
	h := newHarness(t, true, func(c *Config) {
		c.FlatRewardPerBlock = 4.0
		c.PulsePayee = func(owner string, height uint64) string {
			askedHeight = height
			if owner == alice {
				return escrow
			}
			return owner
		}
	})
	h.ledger.add(t, alice, 3)
	h.ledger.add(t, bob, 1)
	startNonce := h.funder(t).Nonce
	h.d.tick()
	h.noFailStop(t)

	blk := h.tip(t)
	if askedHeight != blk.Height {
		t.Fatalf("payee asked for height %d, block %d", askedHeight, blk.Height)
	}
	got := map[string]float64{}
	for _, tx := range blk.Transactions {
		got[tx.Recipient] = tx.Amount
		if tx.Recipient == escrow {
			sum := sha256.Sum256(tx.Payload)
			want := fmt.Sprintf("solo-reward-%d-%s-%s", startNonce, escrow, hex.EncodeToString(sum[:8]))
			if tx.ID != want {
				t.Errorf("escrow reward ID %q, want %q", tx.ID, want)
			}
			if tx.ContractID != chain.MiningRewardContractID || tx.Sender != FunderAddress {
				t.Errorf("escrow reward fields %+v", tx)
			}
		}
	}
	if got[escrow] != 3.0 || got[bob] != 1.0 || got[alice] != 0 {
		t.Fatalf("recipients %v", got)
	}
}

func TestPayeeFallsBackToTheOwner(t *testing.T) {
	h := newHarness(t, true, func(c *Config) {
		c.PulsePayee = func(string, uint64) string { return "" }
	})
	if p := h.d.payee("qsdm1alice", 7); p != "qsdm1alice" {
		t.Fatalf("empty payee answer gave %q", p)
	}
	h2 := newHarness(t, true)
	if p := h2.d.payee("qsdm1alice", 7); p != "qsdm1alice" {
		t.Fatalf("nil hook gave %q", p)
	}
}
