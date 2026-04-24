package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Account represents a user's on-chain state.
type Account struct {
	Address string  `json:"address"`
	Balance float64 `json:"balance"`
	Nonce   uint64  `json:"nonce"`
}

// AccountStore manages all account states and enforces nonce ordering.
type AccountStore struct {
	mu       sync.RWMutex
	accounts map[string]*Account
}

// NewAccountStore creates an empty account store.
func NewAccountStore() *AccountStore {
	return &AccountStore{accounts: make(map[string]*Account)}
}

// GetOrCreate returns an existing account or creates one with zero balance.
func (as *AccountStore) GetOrCreate(address string) *Account {
	as.mu.Lock()
	defer as.mu.Unlock()
	acc, ok := as.accounts[address]
	if !ok {
		acc = &Account{Address: address}
		as.accounts[address] = acc
	}
	cp := *acc
	return &cp
}

// Get returns a copy of the account, or nil if it doesn't exist.
func (as *AccountStore) Get(address string) (*Account, bool) {
	as.mu.RLock()
	defer as.mu.RUnlock()
	acc, ok := as.accounts[address]
	if !ok {
		return nil, false
	}
	cp := *acc
	return &cp, true
}

// DebitAndBumpNonce atomically validates the sender's nonce, checks
// balance, debits `amount`, and bumps the sender's nonce by one.
// Intended for non-transfer transactions (enrollment, future contract
// calls) that go through AccountStore but don't match the
// Sender→Recipient transfer shape of ApplyTx.
//
// Returns an error — with the store UN-mutated — if:
//   - the sender account doesn't exist
//   - tx.Nonce doesn't match the sender's current nonce
//   - the sender's balance is below `amount`
//   - `amount` is non-positive (to keep the contract symmetric with
//     Debit; a zero-amount "just bump the nonce" caller should use a
//     future dedicated helper, not smuggle it through here)
//
// Matches the atomicity guarantee of ApplyTx: either the whole
// debit-and-bump happens, or nothing changes. Required because
// consensus receipts reference the post-apply nonce; any code path
// that bumps the nonce without the debit (or vice-versa) would
// produce inconsistent state roots across validators.
func (as *AccountStore) DebitAndBumpNonce(sender string, amount float64, expectedNonce uint64) error {
	if amount <= 0 {
		return fmt.Errorf("debit amount must be positive, got %.8f", amount)
	}
	as.mu.Lock()
	defer as.mu.Unlock()

	acc, ok := as.accounts[sender]
	if !ok {
		return fmt.Errorf("sender %s not found", sender)
	}
	if acc.Nonce != expectedNonce {
		return fmt.Errorf("nonce mismatch for %s: expected %d, got %d",
			sender, acc.Nonce, expectedNonce)
	}
	if acc.Balance < amount {
		return fmt.Errorf("insufficient balance: have %.8f, need %.8f",
			acc.Balance, amount)
	}
	acc.Balance -= amount
	acc.Nonce++
	return nil
}

// Debit removes funds if the account exists and has sufficient balance.
func (as *AccountStore) Debit(address string, amount float64) error {
	if amount <= 0 {
		return fmt.Errorf("debit amount must be positive")
	}
	as.mu.Lock()
	defer as.mu.Unlock()
	acc, ok := as.accounts[address]
	if !ok {
		return fmt.Errorf("account %s not found", address)
	}
	if acc.Balance < amount {
		return fmt.Errorf("insufficient balance for %s", address)
	}
	acc.Balance -= amount
	return nil
}

// Credit adds funds to an account (e.g. genesis allocation, rewards).
func (as *AccountStore) Credit(address string, amount float64) {
	as.mu.Lock()
	defer as.mu.Unlock()
	acc, ok := as.accounts[address]
	if !ok {
		acc = &Account{Address: address}
		as.accounts[address] = acc
	}
	acc.Balance += amount
}

// ApplyTx validates and applies a transaction: checks balance, nonce, and transfers funds.
func (as *AccountStore) ApplyTx(tx *mempool.Tx) error {
	as.mu.Lock()
	defer as.mu.Unlock()

	sender, ok := as.accounts[tx.Sender]
	if !ok {
		return fmt.Errorf("sender %s not found", tx.Sender)
	}

	// Nonce check: tx.Nonce must equal sender's current nonce
	if tx.Nonce != sender.Nonce {
		return fmt.Errorf("nonce mismatch for %s: expected %d, got %d", tx.Sender, sender.Nonce, tx.Nonce)
	}

	total := tx.Amount + tx.Fee
	if sender.Balance < total {
		return fmt.Errorf("insufficient balance: have %.8f, need %.8f", sender.Balance, total)
	}

	// Apply
	sender.Balance -= total
	sender.Nonce++

	recipient, ok := as.accounts[tx.Recipient]
	if !ok {
		recipient = &Account{Address: tx.Recipient}
		as.accounts[tx.Recipient] = recipient
	}
	recipient.Balance += tx.Amount

	return nil
}

// Clone returns an independent copy of the account store for speculative block building.
// Mutations on the clone do not affect the receiver.
func (as *AccountStore) Clone() *AccountStore {
	as.mu.RLock()
	defer as.mu.RUnlock()
	out := &AccountStore{accounts: make(map[string]*Account, len(as.accounts))}
	for k, v := range as.accounts {
		cp := *v
		out.accounts[k] = &cp
	}
	return out
}

// ChainReplayClone implements ChainReplayApplier.
func (as *AccountStore) ChainReplayClone() ChainReplayApplier {
	if as == nil {
		return nil
	}
	return as.Clone()
}

// RestoreFromChainReplay implements ChainReplayApplier.
func (as *AccountStore) RestoreFromChainReplay(from ChainReplayApplier) error {
	if as == nil {
		return fmt.Errorf("chain: nil account store")
	}
	other, ok := from.(*AccountStore)
	if !ok || other == nil {
		return fmt.Errorf("chain: replay restore expects *AccountStore snapshot")
	}
	as.RestoreFrom(other)
	return nil
}

// RestoreFrom replaces all accounts with a deep copy of snap's map (rollback / atomic apply helper).
func (as *AccountStore) RestoreFrom(snap *AccountStore) {
	if as == nil || snap == nil {
		return
	}
	snap.mu.RLock()
	out := make(map[string]*Account, len(snap.accounts))
	for k, v := range snap.accounts {
		cp := *v
		out[k] = &cp
	}
	snap.mu.RUnlock()
	as.mu.Lock()
	defer as.mu.Unlock()
	as.accounts = out
}

// StateRoot computes a deterministic hash of all account states.
func (as *AccountStore) StateRoot() string {
	as.mu.RLock()
	defer as.mu.RUnlock()

	addrs := make([]string, 0, len(as.accounts))
	for addr := range as.accounts {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)

	h := sha256.New()
	for _, addr := range addrs {
		acc := as.accounts[addr]
		fmt.Fprintf(h, "%s:%f:%d;", acc.Address, acc.Balance, acc.Nonce)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// AllAccounts returns a snapshot of all accounts.
func (as *AccountStore) AllAccounts() []Account {
	as.mu.RLock()
	defer as.mu.RUnlock()
	out := make([]Account, 0, len(as.accounts))
	for _, acc := range as.accounts {
		out = append(out, *acc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// Count returns the number of accounts.
func (as *AccountStore) Count() int {
	as.mu.RLock()
	defer as.mu.RUnlock()
	return len(as.accounts)
}

// Save persists all accounts to a JSON file.
func (as *AccountStore) Save(path string) error {
	accounts := as.AllAccounts()
	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Load restores accounts from a JSON file.
func (as *AccountStore) Load(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var accounts []Account
	if err := json.Unmarshal(data, &accounts); err != nil {
		return 0, err
	}
	as.mu.Lock()
	defer as.mu.Unlock()
	for _, acc := range accounts {
		cp := acc
		as.accounts[acc.Address] = &cp
	}
	return len(accounts), nil
}
