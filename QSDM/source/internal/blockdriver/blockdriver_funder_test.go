package blockdriver

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// GHSA-49pr variant: a funder that cannot cover the due rewards must not fail
// every block (which ends in the no-seal FREEZE). The payouts stay owed, a
// heartbeat is sealed, and the rewards are paid once the funder is topped up.
func TestTick_UnderfundedFunderRetainsPayoutsAndSealsHeartbeat(t *testing.T) {
	h := newHarness(t, true, func(c *Config) {
		c.FlatRewardPerBlock = 3.0
		c.FunderInitialBalance = 1.0
	})
	h.ledger.add(t, "qsdm1alice", 2)
	h.d.tick()
	h.noFreeze(t)
	h.noFailStop(t)
	blk := h.tip(t) // fails the test when no block was sealed
	assertHeartbeatOnly(t, blk)
	if got := h.ledger.Outstanding(); got != 2 {
		t.Fatalf("outstanding %d, want the 2 payouts retained", got)
	}
	if got := h.d.Stats().UnderfundedTicks; got != 1 {
		t.Fatalf("UnderfundedTicks %d, want 1", got)
	}
	if h.pool.Size() != 0 {
		t.Fatalf("pool holds %d txs after the tick, want 0 (no stale reward tx)", h.pool.Size())
	}

	// The funder is topped up: the retained rewards are paid on the next tick.
	h.accounts.Credit(FunderAddress, 100)
	h.d.tick()
	h.noFreeze(t)
	blk = h.tip(t)
	paid := 0.0
	for _, tx := range blk.Transactions {
		if tx.ContractID == chain.MiningRewardContractID {
			paid += tx.Amount
		}
	}
	if paid != 3.0 {
		t.Fatalf("paid %v after the top-up, want 3", paid)
	}
	if got := h.ledger.Outstanding(); got != 0 {
		t.Fatalf("outstanding %d after the top-up, want 0", got)
	}
	if got := h.d.Stats().UnderfundedTicks; got != 1 {
		t.Fatalf("UnderfundedTicks %d, want still 1", got)
	}
}

func TestFunderCovers_SequentialTotal(t *testing.T) {
	h := newHarness(t, true, func(c *Config) { c.FunderInitialBalance = 5.0 })
	bal := h.funder(t).Balance
	mk := func(amts ...float64) []*mempool.Tx {
		out := make([]*mempool.Tx, 0, len(amts))
		for _, a := range amts {
			out = append(out, &mempool.Tx{Sender: FunderAddress, Amount: a})
		}
		return out
	}
	if !h.d.funderCovers(mk(bal/2, bal/2)) {
		t.Fatal("exactly the balance must be covered")
	}
	if h.d.funderCovers(mk(bal/2, bal/2, 0.5)) {
		t.Fatal("more than the balance in total must not be covered")
	}
	other := mk(bal * 10)
	other[0].Sender = "qsdm1someone"
	if !h.d.funderCovers(other) {
		t.Fatal("txs not sent by the funder do not count")
	}
}
