package chain

import (
	"fmt"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"strings"
)

// Tx ID prefixes reserved for the producer's own mining reward and heartbeat
// transactions (HL1, internal/legacymining RewardIDPrefix and
// HeartbeatIDPrefix, which this package cannot import). Wallet admission
// refuses them so no user transfer can take an ID the producer will use.
// Admission only: block replay and apply rules are unchanged.
var reservedWalletTransferIDPrefixes = [...]string{"solo-reward-", "solo-heartbeat-"}

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
	for _, prefix := range reservedWalletTransferIDPrefixes {
		if strings.HasPrefix(tx.ID, prefix) {
			return fmt.Errorf("chain: wallet transfer ID uses reserved prefix %q: %w", prefix, mempool.ErrDuplicateTx)
		}
	}
	bp := s.producer
	bp.sealLifecycleMu.Lock()
	defer bp.sealLifecycleMu.Unlock()
	bp.mu.Lock()
	receipts, pool := bp.appendReceipts, bp.pool
	// The PoE index must describe the committed tip the transfer is checked
	// against; the lifecycle lock keeps that tip fixed until Add returns.
	bp.ensurePoEHistoryLocked()
	bp.mu.Unlock()
	if receipts == nil || pool == nil {
		return fmt.Errorf("chain: wallet transfer receipt store and mempool are required")
	}
	// Any recorded ID is reserved, even if its prior execution failed or used
	// another contract. Reusing it would replace receipt history by TxID.
	if _, exists := receipts.Get(tx.ID); exists {
		return mempool.ErrDuplicateTx
	}
	// Proof-of-Entanglement parent rules against the committed tip (poe.go):
	// the same check block production and every validator's replay apply.
	// Before the activation height this rejects nothing; while a height is
	// configured but not reached it counts what enforcement would reject.
	if err := bp.CheckWalletTransferAdmission(tx); err != nil {
		return err
	}
	// An admission callback may read the producer, so do not hold bp.mu here.
	// The lifecycle lock still closes the drain-to-receipt gap.
	if err := pool.Add(tx); err != nil {
		return err
	}
	bp.shadowCheckWalletTransfer(tx)
	return nil
}
