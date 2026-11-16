package legacymining

import (
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// PULSE: a reward to a member's escrow account pays that member's own IDs.
// PreSeal accepts it (I6 compares the row owner with the member the escrow
// account names), H8 records the payment under the member, and an escrow
// account naming anyone else is refused before sealing.
func TestLedgerV2PulseEscrowRecipient(t *testing.T) {
	pool := strings.Repeat("d", 64)
	h, _ := newLedgerHarnessV2(t, true)
	h.accept(ltMinerA)
	b := h.accept(ltMinerB)
	cell := h.rewardCell(h.height + 1)

	// B's ID in a tx to C's escrow account: refused (I6).
	claims := h.l.Take()
	txs := h.build(claims, cell)
	txs[1] = ltRewardTx(txs[1].Nonce, chain.PulseEscrowAddress(pool, ltMinerC), txs[1].Amount, b)
	err := h.l.PreSeal(h.height+1, cell, txs)
	if !errors.Is(err, ErrPreSeal) || !strings.Contains(err.Error(), "belongs to "+ltMinerB+", not the recipient") {
		t.Fatalf("PreSeal to another member's escrow = %v", err)
	}
	h.firstFreeze(CausePreSeal + ":")
}

func TestLedgerV2PulseEscrowPaysTheMember(t *testing.T) {
	pool := strings.Repeat("d", 64)
	h, _ := newLedgerHarnessV2(t, true)
	h.accept(ltMinerA)
	b := h.accept(ltMinerB)
	cell := h.rewardCell(h.height + 1)
	claims := h.l.Take()
	txs := h.build(claims, cell)
	escrow := chain.PulseEscrowAddress(pool, ltMinerB)
	txs[1] = ltRewardTx(txs[1].Nonce, escrow, txs[1].Amount, b)
	if err := h.l.PreSeal(h.height+1, cell, txs); err != nil {
		t.Fatalf("PreSeal with B's escrow: %v", err)
	}
	if _, err := h.seal(true, txs...); err != nil {
		t.Fatalf("seal: %v", err)
	}
	h.noFreeze()
	if r := h.s.rows[b]; r == nil || r.PaidTxID != txs[1].ID || r.MinerAddr != ltMinerB {
		t.Fatalf("B's proof row after H8: %+v", r)
	}
	if acc, ok := h.accts.Get(escrow); !ok || acc.Balance != txs[1].Amount {
		t.Fatalf("escrow balance %+v", acc)
	}
	// The member's epoch counts the escrowed reward (the admission cap is the
	// proof finder's, as before PULSE).
	if h.l.epochCell[ltMinerB] != txs[1].Amount || h.l.epochCell[escrow] != 0 {
		t.Fatalf("epoch emitted %v", h.l.epochCell)
	}
}

// I7: a block carrying a PULSE tx is clean (a family the applier admits
// but I7 does not would FREEZE mining on the producer's own seal).
func TestAuditTxFamiliesAdmitsPulse(t *testing.T) {
	blk := &chain.Block{Height: 5, Transactions: []*mempool.Tx{{ID: "p", Sender: strings.Repeat("e", 64), ContractID: chain.PulseContractID}}}
	if v := AuditTxFamilies(blk); len(v) != 0 {
		t.Fatalf("PULSE tx flagged by I7: %+v", v)
	}
	blk.Transactions[0].ContractID = "qsdm/pulse/v2"
	if v := AuditTxFamilies(blk); len(v) != 1 {
		t.Fatal("an unknown family was admitted")
	}
}
