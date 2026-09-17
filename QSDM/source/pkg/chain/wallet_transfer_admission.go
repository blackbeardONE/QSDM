package chain

import (
	"fmt"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// walletTransferSubmitter serializes new wallet ingress with the complete block
// lifecycle, including receipt creation and persistence. It does not change
// historical block replay rules.
type walletTransferSubmitter struct{ producer *BlockProducer }

// WalletTransferSubmitter returns an Add-compatible adapter for wallet ingress.
// Configure SetAppendReceiptStore before serving it. Other transaction families
// must continue to use their own admission paths.
func (bp *BlockProducer) WalletTransferSubmitter() *walletTransferSubmitter {
	return &walletTransferSubmitter{producer: bp}
}

func (s *walletTransferSubmitter) Add(tx *mempool.Tx) error {
	if s == nil || s.producer == nil {
		return fmt.Errorf("chain: wallet transfer producer unavailable")
	}
	if tx == nil || tx.ID == "" {
		return fmt.Errorf("chain: wallet transfer transaction and ID are required")
	}
	if tx.ContractID != WalletTransferContractID {
		return fmt.Errorf("chain: wallet transfer admission requires contract_id %q", WalletTransferContractID)
	}
	bp := s.producer
	bp.sealLifecycleMu.Lock()
	defer bp.sealLifecycleMu.Unlock()
	bp.mu.Lock()
	receipts, pool := bp.appendReceipts, bp.pool
	bp.mu.Unlock()
	if receipts == nil || pool == nil {
		return fmt.Errorf("chain: wallet transfer receipt store and mempool are required")
	}
	// Any recorded ID is reserved, even if its prior execution failed or used
	// another contract. Reusing it would replace receipt history by TxID.
	if _, exists := receipts.Get(tx.ID); exists {
		return mempool.ErrDuplicateTx
	}
	// An admission callback may read the producer, so do not hold bp.mu here.
	// The lifecycle lock still closes the drain-to-receipt gap.
	return pool.Add(tx)
}
