package contracts

import (
	"fmt"
	"sync"
)

const (
	DefaultGasLimit    int64 = 1_000_000
	GasPerWASMCall     int64 = 500
	GasPerSimOp        int64 = 100
	GasPerStateRead    int64 = 50
	GasPerStateWrite   int64 = 200
	GasPerByteInput    int64 = 1
	GasPerByteOutput   int64 = 1
	GasBaseDeployment  int64 = 10_000
	GasPerByteCode     int64 = 5
)

// GasMeter tracks gas consumption for a single execution.
type GasMeter struct {
	limit    int64
	consumed int64
	mu       sync.Mutex
}

// NewGasMeter creates a meter with the given gas limit.
func NewGasMeter(limit int64) *GasMeter {
	if limit <= 0 {
		limit = DefaultGasLimit
	}
	return &GasMeter{limit: limit}
}

// Consume charges gas. Returns an error if the budget is exhausted.
func (gm *GasMeter) Consume(amount int64) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.consumed += amount
	if gm.consumed > gm.limit {
		return fmt.Errorf("out of gas: consumed %d, limit %d", gm.consumed, gm.limit)
	}
	return nil
}

// Consumed returns the total gas consumed so far.
func (gm *GasMeter) Consumed() int64 {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	return gm.consumed
}

// Remaining returns how much gas is left.
func (gm *GasMeter) Remaining() int64 {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	r := gm.limit - gm.consumed
	if r < 0 {
		return 0
	}
	return r
}

// Limit returns the gas limit.
func (gm *GasMeter) Limit() int64 {
	return gm.limit
}

// GasConfig holds per-engine gas settings.
type GasConfig struct {
	DefaultLimit    int64 `json:"default_limit"`
	MaxLimit        int64 `json:"max_limit"`
	PricePerGasUnit int64 `json:"price_per_gas_unit"` // smallest token units per gas
}

// DefaultGasConfig returns a sensible default configuration.
func DefaultGasConfig() GasConfig {
	return GasConfig{
		DefaultLimit:    DefaultGasLimit,
		MaxLimit:        10_000_000,
		PricePerGasUnit: 1,
	}
}

// DeploymentGas calculates gas required to deploy a contract with the given code size.
func DeploymentGas(codeLen int) int64 {
	return GasBaseDeployment + int64(codeLen)*GasPerByteCode
}
