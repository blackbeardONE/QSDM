package bridge

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// FeeConfig defines the fee structure for cross-chain transfers.
type FeeConfig struct {
	BaseFee       float64 `json:"base_fee"`       // flat fee per transfer (in native units)
	PercentageFee float64 `json:"percentage_fee"`  // fraction of transfer amount (0.001 = 0.1%)
	MinFee        float64 `json:"min_fee"`         // floor fee
	MaxFee        float64 `json:"max_fee"`         // ceiling fee (0 = uncapped)
}

// DefaultFeeConfig returns a sensible default fee schedule.
func DefaultFeeConfig() FeeConfig {
	return FeeConfig{
		BaseFee:       0.01,
		PercentageFee: 0.001,
		MinFee:        0.01,
		MaxFee:        100.0,
	}
}

// FeeRecord tracks a fee collected for a specific lock/transfer.
type FeeRecord struct {
	LockID     string    `json:"lock_id"`
	Amount     float64   `json:"amount"`
	FeeCharged float64   `json:"fee_charged"`
	NetAmount  float64   `json:"net_amount"`
	Timestamp  time.Time `json:"timestamp"`
}

// FeeCollector manages fee computation, collection, and distribution.
type FeeCollector struct {
	mu           sync.RWMutex
	config       FeeConfig
	collected    []FeeRecord
	totalFees    float64
	distributed  float64
	recipients   map[string]float64 // address -> share (fractions summing to 1.0)
	balances     map[string]float64 // address -> accumulated pending payout
}

// NewFeeCollector creates a fee collector with the given config and distribution targets.
func NewFeeCollector(cfg FeeConfig) *FeeCollector {
	return &FeeCollector{
		config:     cfg,
		collected:  make([]FeeRecord, 0, 64),
		recipients: make(map[string]float64),
		balances:   make(map[string]float64),
	}
}

// SetConfig updates the fee config at runtime.
func (fc *FeeCollector) SetConfig(cfg FeeConfig) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.config = cfg
}

// SetDistribution sets fee distribution recipients with their share fractions.
// Shares must sum to approximately 1.0.
func (fc *FeeCollector) SetDistribution(recipients map[string]float64) error {
	total := 0.0
	for _, share := range recipients {
		if share < 0 {
			return fmt.Errorf("negative share not allowed")
		}
		total += share
	}
	if math.Abs(total-1.0) > 0.001 {
		return fmt.Errorf("shares must sum to 1.0, got %f", total)
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.recipients = make(map[string]float64)
	for addr, share := range recipients {
		fc.recipients[addr] = share
	}
	return nil
}

// CalculateFee computes the fee for a given transfer amount without recording it.
func (fc *FeeCollector) CalculateFee(amount float64) float64 {
	fc.mu.RLock()
	cfg := fc.config
	fc.mu.RUnlock()

	fee := cfg.BaseFee + amount*cfg.PercentageFee
	if fee < cfg.MinFee {
		fee = cfg.MinFee
	}
	if cfg.MaxFee > 0 && fee > cfg.MaxFee {
		fee = cfg.MaxFee
	}
	return math.Round(fee*1e8) / 1e8 // 8 decimal places
}

// Collect records a fee for a transfer, distributes to recipients, and returns the record.
func (fc *FeeCollector) Collect(lockID string, amount float64) FeeRecord {
	fee := fc.CalculateFee(amount)
	net := amount - fee
	if net < 0 {
		net = 0
	}
	rec := FeeRecord{
		LockID:     lockID,
		Amount:     amount,
		FeeCharged: fee,
		NetAmount:  net,
		Timestamp:  time.Now(),
	}

	fc.mu.Lock()
	fc.collected = append(fc.collected, rec)
	fc.totalFees += fee

	for addr, share := range fc.recipients {
		fc.balances[addr] += fee * share
	}
	fc.mu.Unlock()

	return rec
}

// TotalCollected returns the total fees collected so far.
func (fc *FeeCollector) TotalCollected() float64 {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.totalFees
}

// PendingBalance returns the accumulated undistributed balance for an address.
func (fc *FeeCollector) PendingBalance(addr string) float64 {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.balances[addr]
}

// Withdraw claims the pending balance for an address, returning the amount.
func (fc *FeeCollector) Withdraw(addr string) float64 {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	amt := fc.balances[addr]
	fc.distributed += amt
	fc.balances[addr] = 0
	return amt
}

// History returns all fee records, most recent first, up to `limit`.
func (fc *FeeCollector) History(limit int) []FeeRecord {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	if limit <= 0 || limit > len(fc.collected) {
		limit = len(fc.collected)
	}
	out := make([]FeeRecord, limit)
	for i := 0; i < limit; i++ {
		out[i] = fc.collected[len(fc.collected)-1-i]
	}
	return out
}

// Stats returns aggregate fee statistics.
func (fc *FeeCollector) Stats() map[string]interface{} {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return map[string]interface{}{
		"total_collected":  fc.totalFees,
		"total_distributed": fc.distributed,
		"pending":           fc.totalFees - fc.distributed,
		"record_count":      len(fc.collected),
	}
}
