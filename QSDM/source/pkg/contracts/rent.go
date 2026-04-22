package contracts

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// RentConfig defines per-byte storage rent parameters.
type RentConfig struct {
	CostPerBytePerDay float64       `json:"cost_per_byte_per_day"` // rent in native units
	GracePeriod       time.Duration `json:"grace_period"`          // how long before eviction after balance exhaustion
	MinDeposit        float64       `json:"min_deposit"`           // minimum deposit to deploy
	EvictInterval     time.Duration `json:"evict_interval"`        // sweep interval
}

// DefaultRentConfig returns sensible defaults.
func DefaultRentConfig() RentConfig {
	return RentConfig{
		CostPerBytePerDay: 0.000001,
		GracePeriod:       7 * 24 * time.Hour,
		MinDeposit:        0.01,
		EvictInterval:     1 * time.Hour,
	}
}

// RentAccount tracks rent deposit and charges for a contract.
type RentAccount struct {
	ContractID    string    `json:"contract_id"`
	Deposit       float64   `json:"deposit"`
	TotalCharged  float64   `json:"total_charged"`
	LastChargedAt time.Time `json:"last_charged_at"`
	StorageBytes  int64     `json:"storage_bytes"`
	GraceStart    time.Time `json:"grace_start,omitempty"` // when grace began (zero if not in grace)
	Evicted       bool      `json:"evicted"`
}

// RentManager charges per-byte storage rent and evicts dormant contracts.
type RentManager struct {
	mu       sync.RWMutex
	engine   *ContractEngine
	accounts map[string]*RentAccount
	config   RentConfig
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewRentManager creates a rent manager for the given engine.
func NewRentManager(engine *ContractEngine, cfg RentConfig) *RentManager {
	return &RentManager{
		engine:   engine,
		accounts: make(map[string]*RentAccount),
		config:   cfg,
		stopCh:   make(chan struct{}),
	}
}

// RegisterContract creates a rent account for a newly deployed contract.
func (rm *RentManager) RegisterContract(contractID string, deposit float64) error {
	if deposit < rm.config.MinDeposit {
		return fmt.Errorf("deposit %.8f below minimum %.8f", deposit, rm.config.MinDeposit)
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if _, exists := rm.accounts[contractID]; exists {
		return fmt.Errorf("rent account already exists for %s", contractID)
	}
	storageBytes := rm.estimateStorage(contractID)
	rm.accounts[contractID] = &RentAccount{
		ContractID:    contractID,
		Deposit:       deposit,
		LastChargedAt: time.Now(),
		StorageBytes:  storageBytes,
	}
	return nil
}

// TopUp adds funds to a contract's rent deposit.
func (rm *RentManager) TopUp(contractID string, amount float64) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	acc, ok := rm.accounts[contractID]
	if !ok {
		return fmt.Errorf("no rent account for %s", contractID)
	}
	if acc.Evicted {
		return fmt.Errorf("contract %s has been evicted", contractID)
	}
	acc.Deposit += amount
	acc.GraceStart = time.Time{} // clear grace if topped up
	return nil
}

// ChargeAll sweeps all accounts and charges outstanding rent.
func (rm *RentManager) ChargeAll() (charged int, evicted int) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	now := time.Now()

	for _, acc := range rm.accounts {
		if acc.Evicted {
			continue
		}
		acc.StorageBytes = rm.estimateStorage(acc.ContractID)
		elapsed := now.Sub(acc.LastChargedAt)
		days := elapsed.Hours() / 24.0
		if days < 0.001 {
			continue
		}

		cost := float64(acc.StorageBytes) * rm.config.CostPerBytePerDay * days
		if cost <= 0 {
			acc.LastChargedAt = now
			continue
		}

		if acc.Deposit >= cost {
			acc.Deposit -= cost
			acc.TotalCharged += cost
			acc.LastChargedAt = now
			acc.GraceStart = time.Time{}
			charged++
		} else {
			// Partial charge + enter grace period
			acc.TotalCharged += acc.Deposit
			acc.Deposit = 0
			acc.LastChargedAt = now
			if acc.GraceStart.IsZero() {
				acc.GraceStart = now
			}
			charged++

			if now.Sub(acc.GraceStart) >= rm.config.GracePeriod {
				acc.Evicted = true
				evicted++
			}
		}
	}
	return
}

// GetAccount returns the rent account for a contract.
func (rm *RentManager) GetAccount(contractID string) (*RentAccount, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	acc, ok := rm.accounts[contractID]
	if !ok {
		return nil, false
	}
	cp := *acc
	return &cp, true
}

// ListAccounts returns all rent accounts.
func (rm *RentManager) ListAccounts() []RentAccount {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	out := make([]RentAccount, 0, len(rm.accounts))
	for _, acc := range rm.accounts {
		out = append(out, *acc)
	}
	return out
}

// InGrace returns contracts currently in grace period.
func (rm *RentManager) InGrace() []RentAccount {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	var out []RentAccount
	for _, acc := range rm.accounts {
		if !acc.Evicted && !acc.GraceStart.IsZero() {
			out = append(out, *acc)
		}
	}
	return out
}

// Start begins the background rent-charging loop.
func (rm *RentManager) Start() {
	interval := rm.config.EvictInterval
	if interval <= 0 {
		interval = time.Hour
	}
	rm.wg.Add(1)
	go func() {
		defer rm.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-rm.stopCh:
				return
			case <-ticker.C:
				rm.ChargeAll()
			}
		}
	}()
}

// Stop halts the background loop.
func (rm *RentManager) Stop() {
	close(rm.stopCh)
	rm.wg.Wait()
}

func (rm *RentManager) estimateStorage(contractID string) int64 {
	rm.engine.mu.RLock()
	contract, exists := rm.engine.contracts[contractID]
	rm.engine.mu.RUnlock()
	if !exists {
		return 0
	}
	size := int64(len(contract.Code))
	if contract.State != nil {
		stateJSON, _ := json.Marshal(contract.State)
		size += int64(len(stateJSON))
	}
	if contract.ABI != nil {
		abiJSON, _ := json.Marshal(contract.ABI)
		size += int64(len(abiJSON))
	}
	return size
}
