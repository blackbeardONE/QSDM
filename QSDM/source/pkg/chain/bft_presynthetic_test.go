package chain

import (
	"testing"
	"time"
)

func TestRunSyntheticBFTRoundWithExecutor(t *testing.T) {
	bc, vs := setupBFT(t)
	ex := NewBFTExecutor(bc)
	sr := "proposal-root-1"
	blk := &Block{
		Height: 1, PrevHash: "", Timestamp: time.Unix(1700000000, 0),
		Transactions: nil, StateRoot: sr, TotalFees: 0, GasUsed: 0, ProducerID: "node",
	}
	blk.Hash = computeBlockHash(blk)
	if err := RunSyntheticBFTRoundWithExecutor(ex, vs, blk); err != nil {
		t.Fatal(err)
	}
	if !bc.IsCommitted(1) {
		t.Fatal("expected height 1 committed")
	}
	if err := RunSyntheticBFTRoundWithExecutor(ex, vs, blk); err != nil {
		t.Fatalf("second call should noop committed height: %v", err)
	}
}
