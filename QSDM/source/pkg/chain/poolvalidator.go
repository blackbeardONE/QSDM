package chain

import (
	"fmt"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// TxValidator pre-validates transactions against account state before they
// enter the mempool, rejecting invalid nonces and insufficient balances early.
type TxValidator struct {
	mu       sync.RWMutex
	accounts *AccountStore
	pending  map[string]uint64  // address -> highest pending nonce (accounts nonce + queued count)
	reserved map[string]float64 // address -> total amount+fee reserved by pending txs
}

// NewTxValidator creates a validator backed by the given account store.
func NewTxValidator(accounts *AccountStore) *TxValidator {
	return &TxValidator{
		accounts: accounts,
		pending:  make(map[string]uint64),
		reserved: make(map[string]float64),
	}
}

// Validate checks a transaction against current account state and pending pool state.
// Returns nil if the tx is acceptable for the mempool.
func (tv *TxValidator) Validate(tx *mempool.Tx) error {
	if tx.Sender == "" {
		return fmt.Errorf("empty sender")
	}
	if tx.Recipient == "" {
		return fmt.Errorf("empty recipient")
	}
	if tx.Amount < 0 {
		return fmt.Errorf("negative amount")
	}
	if tx.Fee < 0 {
		return fmt.Errorf("negative fee")
	}

	acc, ok := tv.accounts.Get(tx.Sender)
	if !ok {
		return fmt.Errorf("sender %s not found", tx.Sender)
	}

	tv.mu.RLock()
	expectedNonce := acc.Nonce
	if pending, hasPending := tv.pending[tx.Sender]; hasPending && pending > expectedNonce {
		expectedNonce = pending
	}
	reserved := tv.reserved[tx.Sender]
	tv.mu.RUnlock()

	if tx.Nonce != expectedNonce {
		return fmt.Errorf("nonce mismatch for %s: expected %d, got %d", tx.Sender, expectedNonce, tx.Nonce)
	}

	total := tx.Amount + tx.Fee
	available := acc.Balance - reserved
	if available < total {
		return fmt.Errorf("insufficient available balance: have %.8f (%.8f reserved), need %.8f",
			acc.Balance, reserved, total)
	}

	return nil
}

// Accept marks a transaction as accepted into the mempool, updating pending state.
func (tv *TxValidator) Accept(tx *mempool.Tx) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	nextNonce := tx.Nonce + 1
	if current, ok := tv.pending[tx.Sender]; !ok || nextNonce > current {
		tv.pending[tx.Sender] = nextNonce
	}
	tv.reserved[tx.Sender] += tx.Amount + tx.Fee
}

// Remove clears a transaction from pending tracking (e.g. after block inclusion or eviction).
func (tv *TxValidator) Remove(tx *mempool.Tx) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	tv.reserved[tx.Sender] -= tx.Amount + tx.Fee
	if tv.reserved[tx.Sender] <= 0 {
		delete(tv.reserved, tx.Sender)
	}
}

// Reset clears all pending state (e.g. after a new block is committed).
func (tv *TxValidator) Reset() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.pending = make(map[string]uint64)
	tv.reserved = make(map[string]float64)
}

// ValidateAndAdd validates a transaction and, if valid, adds it to the mempool.
func (tv *TxValidator) ValidateAndAdd(pool *mempool.Mempool, tx *mempool.Tx) error {
	if err := tv.Validate(tx); err != nil {
		return err
	}
	if err := pool.Add(tx); err != nil {
		return err
	}
	tv.Accept(tx)
	return nil
}

// PendingNonce returns the next expected nonce for an address (including queued txs).
func (tv *TxValidator) PendingNonce(address string) uint64 {
	tv.mu.RLock()
	if n, ok := tv.pending[address]; ok {
		tv.mu.RUnlock()
		return n
	}
	tv.mu.RUnlock()

	acc, ok := tv.accounts.Get(address)
	if !ok {
		return 0
	}
	return acc.Nonce
}

// ReservedBalance returns the total amount reserved by pending txs for an address.
func (tv *TxValidator) ReservedBalance(address string) float64 {
	tv.mu.RLock()
	defer tv.mu.RUnlock()
	return tv.reserved[address]
}
