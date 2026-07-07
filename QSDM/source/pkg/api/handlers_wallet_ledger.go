package api

import "sync"

// LocalWalletTransferLedger is the write-capable counterpart to
// MiningAccountProbe for solo-validator mode. When wired, wallet reads and
// signed wallet transfers use the live QSDM AccountStore instead of a separate
// storage balance table.
type LocalWalletTransferLedger interface {
	BalanceOf(address string) (balance float64, nonce uint64, present bool)
	ApplyTransfer(txID, sender, recipient string, amount, fee float64, envelopeNonce uint64) error
}

type localWalletTransferLedgerHolder struct {
	mu     sync.RWMutex
	ledger LocalWalletTransferLedger
}

var localWalletTransferHolder = &localWalletTransferLedgerHolder{}

func SetLocalWalletTransferLedger(ledger LocalWalletTransferLedger) {
	localWalletTransferHolder.mu.Lock()
	defer localWalletTransferHolder.mu.Unlock()
	localWalletTransferHolder.ledger = ledger
}

func currentLocalWalletTransferLedger() LocalWalletTransferLedger {
	localWalletTransferHolder.mu.RLock()
	defer localWalletTransferHolder.mu.RUnlock()
	return localWalletTransferHolder.ledger
}
