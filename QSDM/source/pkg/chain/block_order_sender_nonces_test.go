package chain

import (
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// orderSenderNonces had no test at all, and audit §3.1's Mempool row lists
// "orderSenderNonces breaks fee ranking" as a gap. Both halves of that deserve
// pinning, because the second is true in a narrow sense and misleading as a
// defect claim.
//
// What the function must guarantee:
//
//   - Each sender's transactions execute in ascending nonce order. Without this
//     a pair of equal-fee transactions can execute N+1, N; the first fails on a
//     nonce mismatch and the block carries an unrecoverable gap.
//   - The SLOTS each sender occupies are untouched, so which transactions were
//     selected for the block, and the relative order of different senders, are
//     both unchanged.
//
// The consequence the audit calls a gap: because a sender's own transactions are
// reordered within their slots, the block's sequence is no longer globally
// fee-descending. That is not a defect, it is the price of nonce correctness --
// the alternative is dropping the out-of-order transaction, which would change
// selection. Pinned here so the trade is visible and cannot be "fixed" by
// someone reading the audit row without reading the function.
func TestOrderSenderNonces_preservesInterSenderSlotsAndOrdersNoncesWithin(t *testing.T) {
	// Fee-descending selection order, as the mempool heap would hand it over.
	txs := []*mempool.Tx{
		{ID: "a-hi", Sender: "alice", Nonce: 1, Fee: 100},
		{ID: "b-mid", Sender: "bob", Nonce: 0, Fee: 50},
		{ID: "a-lo", Sender: "alice", Nonce: 0, Fee: 10},
		{ID: "c-low", Sender: "carol", Nonce: 0, Fee: 1},
	}
	senderOrderBefore := make([]string, len(txs))
	for i, tx := range txs {
		senderOrderBefore[i] = tx.Sender
	}

	orderSenderNonces(txs)

	// 1. Slot ownership per sender is unchanged -- inter-sender ranking intact.
	for i, tx := range txs {
		if tx.Sender != senderOrderBefore[i] {
			t.Fatalf("slot %d changed sender %q -> %q: orderSenderNonces must not move a "+
				"transaction between senders' slots, or it changes which senders the fee "+
				"ranking selected and in what order", i, senderOrderBefore[i], tx.Sender)
		}
	}

	// 2. Every sender's nonces ascend.
	last := map[string]uint64{}
	seen := map[string]bool{}
	for _, tx := range txs {
		if seen[tx.Sender] && tx.Nonce <= last[tx.Sender] {
			t.Fatalf("sender %q executes nonce %d after %d; a sender's transactions must "+
				"ascend or the first fails on a nonce mismatch and the block carries a gap",
				tx.Sender, tx.Nonce, last[tx.Sender])
		}
		last[tx.Sender], seen[tx.Sender] = tx.Nonce, true
	}

	// 3. The documented consequence: alice's 100-fee tx now sits BELOW her
	//    10-fee tx, so the block is not globally fee-descending. Asserted rather
	//    than lamented -- if someone "fixes" the audit's fee-ranking gap by
	//    restoring fee order here, this fails and points at the nonce guarantee.
	if txs[0].ID != "a-lo" || txs[2].ID != "a-hi" {
		t.Fatalf("expected alice's nonce 0 (fee 10) in her first slot and nonce 1 (fee 100) "+
			"in her second; got slot0=%s slot2=%s. Global fee-descending order is "+
			"deliberately sacrificed within a sender to keep nonces ascending.",
			txs[0].ID, txs[2].ID)
	}
	// The set is preserved: nothing selected was dropped or duplicated.
	ids := map[string]int{}
	for _, tx := range txs {
		ids[tx.ID]++
	}
	for _, want := range []string{"a-hi", "a-lo", "b-mid", "c-low"} {
		if ids[want] != 1 {
			t.Errorf("transaction %s appears %d times, want exactly 1: reordering must not "+
				"drop or duplicate a selected transaction", want, ids[want])
		}
	}
}

// A nil entry or an empty sender must not shift another sender's slots. Both are
// skipped by the grouping pass and by the write-back pass, and those two skips
// have to agree or the write-back walks off its own index.
func TestOrderSenderNonces_toleratesNilAndUnattributedEntries(t *testing.T) {
	txs := []*mempool.Tx{
		{ID: "a1", Sender: "alice", Nonce: 1, Fee: 5},
		nil,
		{ID: "anon", Sender: "", Nonce: 7, Fee: 5},
		{ID: "a0", Sender: "alice", Nonce: 0, Fee: 5},
	}

	orderSenderNonces(txs)

	if txs[1] != nil {
		t.Errorf("nil slot was overwritten: %+v", txs[1])
	}
	if txs[2] == nil || txs[2].ID != "anon" {
		t.Errorf("the unattributed transaction moved out of its slot: %+v", txs[2])
	}
	if txs[0] == nil || txs[0].ID != "a0" || txs[3] == nil || txs[3].ID != "a1" {
		t.Errorf("alice's slots should hold nonce 0 then nonce 1, got %+v / %+v", txs[0], txs[3])
	}
}
