package chain

// txid_unique.go: a transaction ID names one transaction, whatever its contract.
//
// Wallet transfers follow this since the PoE activation height: their admission
// (walletTransferSubmitter.Add) reserves every recorded ID, and a transfer whose
// ID is committed in the reference window is invalid (poe.go, rule 2). Release
// rc1111 holds every contract to the same rule.
//
// Four layers now keep IDs unique:
//
//   - admission: the producer's signed task route admits an action only when no
//     receipt and no other pending transaction holds its ID
//     (taskActionSubmitter.Add, serialized with the block lifecycle like the
//     wallet route);
//   - production: ProduceBlock and ProduceBlockWithReceipts drop a drained
//     transaction whose ID already has a committed receipt, or a failed one of
//     another contract, without writing any receipt for it;
//   - receipts: a stored receipt is replaced only when it is a failed receipt
//     and the new one is for the same contract (a failed transaction resubmitted
//     and now applied); a committed receipt is final;
//   - consensus: from TxIDUniqueActivationHeight a block is invalid when any of
//     its transactions repeats an ID committed in the PoE reference window or
//     earlier in the same block (poeBlockScope.check), the rule wallet transfers
//     already follow. The window index is derived from blocks only, so every
//     node decides the same way. Blocks below the height replay as produced.

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// TxIDUniqueActivationHeight is the first block at which every transaction,
// whatever its contract, must carry an ID that is not committed in the PoE
// reference window or earlier in the same block.
const TxIDUniqueActivationHeight uint64 = 1_160_000

// txIDUniqueFrom holds the activation height in force; tests lower it.
var txIDUniqueFrom atomic.Uint64

func init() { txIDUniqueFrom.Store(TxIDUniqueActivationHeight) }

// TxIDUniqueActiveAt reports whether the all-contract ID rule governs a block at
// height. It needs the PoE window index, so it applies only while PoE is active
// at that height too.
func TxIDUniqueActiveAt(height uint64) bool {
	h := txIDUniqueFrom.Load()
	return h != 0 && height >= h && PoEActiveAt(height)
}

// ErrTxIDTaken: the ID already belongs to another recorded or pending
// transaction. It is deliberately not mempool.ErrDuplicateTx, which callers
// read as "this same transaction is already queued".
var ErrTxIDTaken = errors.New("chain: transaction ID already used by another transaction")

var txIDStats struct {
	admissionRefused  atomic.Uint64
	productionSkipped atomic.Uint64
	receiptsKept      atomic.Uint64
	blocksRefused     atomic.Uint64
	keptOnce          sync.Once
}

// TxIDUniqueStatsSnapshot is the metrics view of the ID rule.
type TxIDUniqueStatsSnapshot struct {
	ActivationHeight  uint64
	AdmissionRefused  uint64
	ProductionSkipped uint64
	ReceiptsKept      uint64
	BlocksRefused     uint64
}

// TxIDUniqueStats returns the counters.
func TxIDUniqueStats() TxIDUniqueStatsSnapshot {
	return TxIDUniqueStatsSnapshot{
		ActivationHeight:  txIDUniqueFrom.Load(),
		AdmissionRefused:  txIDStats.admissionRefused.Load(),
		ProductionSkipped: txIDStats.productionSkipped.Load(),
		ReceiptsKept:      txIDStats.receiptsKept.Load(),
		BlocksRefused:     txIDStats.blocksRefused.Load(),
	}
}

// receiptMayReplace reports whether next may take the place of prev (same
// TxID). Only a failed receipt is replaced, and only by a receipt for the same
// contract: a failed transaction resubmitted and applied later, or the same
// receipt stored again. A committed (successful) receipt is final.
func receiptMayReplace(prev, next *TxReceipt) bool {
	return prev.Status == ReceiptFailed && prev.ContractID == next.ContractID
}

// noteReceiptKept counts a refused replacement and logs the first one. The same
// receipt stored again (a reload, a re-sent block) is not a conflict.
func noteReceiptKept(prev, next *TxReceipt) {
	if prev.BlockHeight == next.BlockHeight && prev.BlockHash == next.BlockHash && prev.Status == next.Status &&
		prev.ContractID == next.ContractID {
		return
	}
	txIDStats.receiptsKept.Add(1)
	txIDStats.keptOnce.Do(func() {
		log.Printf("chain: kept the receipt of %q (contract %q, status %d, block %d); refused a later receipt with the same ID (contract %q, status %d, block %d)",
			prev.TxID, prev.ContractID, prev.Status, prev.BlockHeight, next.ContractID, next.Status, next.BlockHeight)
	})
}

// receiptBlocksInclusion reports whether tx must stay out of a block because its
// ID already belongs to a committed transaction, or to a failed one of another
// contract. A failed transaction of the same contract may be applied again.
func receiptBlocksInclusion(rs *ReceiptStore, tx *mempool.Tx) bool {
	if rs == nil || tx == nil || tx.ID == "" {
		return false
	}
	prev, ok := rs.Get(tx.ID)
	if !ok {
		return false
	}
	return prev.Status == ReceiptSuccess || prev.ContractID != tx.ContractID
}

// dropReceiptCollisions removes from txs (drained for production) every
// transaction receiptBlocksInclusion refuses. They are counted and discarded:
// no receipt is written for them, so the receipt that owns the ID stays.
func dropReceiptCollisions(txs []*mempool.Tx, rs *ReceiptStore) []*mempool.Tx {
	if rs == nil {
		return txs
	}
	kept := txs[:0:0]
	for _, tx := range txs {
		if receiptBlocksInclusion(rs, tx) {
			txIDStats.productionSkipped.Add(1)
			continue
		}
		kept = append(kept, tx)
	}
	return kept
}

// sameMempoolTx reports whether a and b are the same transaction (the same
// signed action submitted again), not merely the same ID.
func sameMempoolTx(a, b *mempool.Tx) bool {
	return a != nil && b != nil && a.ID == b.ID && a.ContractID == b.ContractID && a.Sender == b.Sender &&
		a.Recipient == b.Recipient && a.Amount == b.Amount && a.Nonce == b.Nonce && bytes.Equal(a.Payload, b.Payload)
}

// taskActionSubmitter admits signed task actions under the same block-lifecycle
// lock as wallet transfers, so a receipt cannot appear between the check and
// the admission.
type taskActionSubmitter struct{ producer *BlockProducer }

// TaskActionSubmitter returns an Add-compatible adapter for the signed task
// route. Configure SetAppendReceiptStore before serving it.
func (bp *BlockProducer) TaskActionSubmitter() *taskActionSubmitter {
	return &taskActionSubmitter{producer: bp}
}

func (s *taskActionSubmitter) Add(tx *mempool.Tx) error {
	if s == nil || s.producer == nil {
		return fmt.Errorf("chain: task action producer unavailable")
	}
	if tx == nil || tx.ID == "" {
		return fmt.Errorf("chain: task action transaction and ID are required")
	}
	if tx.ContractID != TaskContractID {
		return fmt.Errorf("chain: task action admission requires contract_id %q", TaskContractID)
	}
	bp := s.producer
	bp.sealLifecycleMu.Lock()
	defer bp.sealLifecycleMu.Unlock()
	bp.mu.Lock()
	receipts, pool := bp.appendReceipts, bp.pool
	bp.ensurePoEHistoryLocked()
	bp.mu.Unlock()
	if receipts == nil || pool == nil {
		return fmt.Errorf("chain: task action receipt store and mempool are required")
	}
	refuse := func(why string) error {
		txIDStats.admissionRefused.Add(1)
		return fmt.Errorf("%w: %q %s", ErrTxIDTaken, tx.ID, why)
	}
	// Any recorded ID is reserved, as on the wallet route: reusing it would
	// replace another transaction's receipt.
	if _, exists := receipts.Get(tx.ID); exists {
		return refuse("already has a receipt")
	}
	if bp.poeHist.committed(tx.ID) {
		return refuse("is committed in the reference window")
	}
	if pending, ok := pool.Get(tx.ID); ok {
		if sameMempoolTx(pending, tx) {
			return mempool.ErrDuplicateTx
		}
		return refuse("is pending for another transaction")
	}
	if err := pool.Add(tx); err != nil {
		if errors.Is(err, mempool.ErrDuplicateTx) {
			// Another admission path queued the ID meanwhile.
			if pending, ok := pool.Get(tx.ID); ok && !sameMempoolTx(pending, tx) {
				return refuse("is pending for another transaction")
			}
		}
		return err
	}
	return nil
}
