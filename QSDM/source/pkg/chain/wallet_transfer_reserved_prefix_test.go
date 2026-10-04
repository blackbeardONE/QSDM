package chain

import (
	"errors"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// HL1: wallet admission refuses the producer's reserved reward and heartbeat
// ID prefixes (design rev 4 §2).
func TestWalletTransferAdmissionRejectsReservedIDPrefixes(t *testing.T) {
	for _, id := range []string{
		"solo-reward-",
		"solo-reward-7-qsdm1miner-0123456789abcdef",
		"solo-reward-7-qsdm1miner",
		"solo-heartbeat-",
		"solo-heartbeat-12-1700000000000000000",
	} {
		t.Run(id, func(t *testing.T) {
			bp, pool, receipts := admissionTestProducer()
			err := bp.WalletTransferSubmitter().Add(admissionTestTx(id, "alice", 0))
			if !errors.Is(err, mempool.ErrDuplicateTx) {
				t.Fatalf("Add(%q) = %v, want a reserved-ID rejection wrapping ErrDuplicateTx", id, err)
			}
			if pool.Size() != 0 || receipts.Count() != 0 {
				t.Fatalf("rejected reserved ID changed pool (%d) or receipts (%d)", pool.Size(), receipts.Count())
			}
		})
	}
}

func TestWalletTransferAdmissionAllowsReservedPrefixLookalikes(t *testing.T) {
	bp, pool, _ := admissionTestProducer()
	s := bp.WalletTransferSubmitter()
	ids := []string{
		"solo-reward",
		"solo-heartbeat",
		"solo-rewards-1",
		"Solo-reward-1",
		"SOLO-HEARTBEAT-1",
		"x-solo-reward-1",
		" solo-heartbeat-1",
		"solo_reward_1",
	}
	for i, id := range ids {
		if err := s.Add(admissionTestTx(id, "alice", uint64(i))); err != nil {
			t.Fatalf("Add(%q) = %v, want admitted", id, err)
		}
	}
	if pool.Size() != len(ids) {
		t.Fatalf("pool size = %d, want %d", pool.Size(), len(ids))
	}
}

// The check runs at admission, before the seal lifecycle lock, so a reserved
// ID is refused even while a seal holds that lock.
func TestWalletTransferReservedPrefixRejectedBeforeLifecycleLock(t *testing.T) {
	bp, _, _ := admissionTestProducer()
	bp.sealLifecycleMu.Lock()
	defer bp.sealLifecycleMu.Unlock()
	done := make(chan error, 1)
	go func() { done <- bp.WalletTransferSubmitter().Add(admissionTestTx("solo-reward-1-x-00", "alice", 0)) }()
	select {
	case err := <-done:
		if !errors.Is(err, mempool.ErrDuplicateTx) {
			t.Fatalf("Add = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reserved-ID rejection waited on sealLifecycleMu")
	}
}

// Admission only: block production and apply do not look at the prefix, so
// the producer's own reserved-ID transactions still seal.
func TestReservedPrefixDoesNotAffectBlockApply(t *testing.T) {
	bp, pool, _ := admissionTestProducer()
	tx := &mempool.Tx{ID: "solo-heartbeat-0-1", Sender: "alice", Recipient: "alice", Amount: 0, Nonce: 0}
	if err := pool.Add(tx); err != nil {
		t.Fatalf("pool.Add: %v", err)
	}
	blk, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if len(blk.Transactions) != 1 || blk.Transactions[0].ID != tx.ID {
		t.Fatalf("reserved-ID tx not sealed: %+v", blk.Transactions)
	}
}
