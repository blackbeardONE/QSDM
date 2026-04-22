package chain

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrAlreadyRegistered  = errors.New("validator already registered")
	ErrNotRegistered      = errors.New("validator not registered")
	ErrInsufficientStake  = errors.New("insufficient stake")
	ErrAlreadySlashed     = errors.New("validator already slashed")
	ErrValidatorJailed    = errors.New("validator is jailed")
)

// ValidatorStatus represents a validator's lifecycle state.
type ValidatorStatus string

const (
	ValidatorActive  ValidatorStatus = "active"
	ValidatorJailed  ValidatorStatus = "jailed"
	ValidatorExited  ValidatorStatus = "exited"
)

// SlashReason describes why a validator was slashed.
type SlashReason string

const (
	SlashDoubleSign    SlashReason = "double_sign"
	SlashDowntime      SlashReason = "downtime"
	SlashInvalidBlock  SlashReason = "invalid_block"
)

// Validator represents a registered block producer.
type Validator struct {
	Address      string          `json:"address"`
	Stake        float64         `json:"stake"`
	Status       ValidatorStatus `json:"status"`
	RegisteredAt time.Time       `json:"registered_at"`
	JailedUntil  time.Time       `json:"jailed_until,omitempty"`
	SlashCount   int             `json:"slash_count"`
	TotalSlashed float64         `json:"total_slashed"`
	BlocksProduced uint64        `json:"blocks_produced"`
	LastBlockAt  time.Time       `json:"last_block_at,omitempty"`
}

// SlashEvent records a slashing incident.
type SlashEvent struct {
	Validator string      `json:"validator"`
	Reason    SlashReason `json:"reason"`
	Amount    float64     `json:"amount"`
	Epoch     uint64      `json:"epoch"`
	Timestamp time.Time   `json:"timestamp"`
}

// ValidatorSetConfig configures the validator set.
type ValidatorSetConfig struct {
	MinStake       float64       `json:"min_stake"`
	MaxValidators  int           `json:"max_validators"`
	EpochBlocks    uint64        `json:"epoch_blocks"`     // blocks per epoch
	JailDuration   time.Duration `json:"jail_duration"`
	SlashFraction  float64       `json:"slash_fraction"`   // fraction of stake slashed (0.0–1.0)
	DowntimeSlash  float64       `json:"downtime_slash"`   // fraction for downtime
}

// DefaultValidatorSetConfig returns sensible defaults.
func DefaultValidatorSetConfig() ValidatorSetConfig {
	return ValidatorSetConfig{
		MinStake:      100.0,
		MaxValidators: 100,
		EpochBlocks:   100,
		JailDuration:  1 * time.Hour,
		SlashFraction: 0.05,
		DowntimeSlash: 0.01,
	}
}

// ValidatorSet manages the active set of validators.
type ValidatorSet struct {
	mu          sync.RWMutex
	validators  map[string]*Validator
	slashLog    []SlashEvent
	config      ValidatorSetConfig
	currentEpoch uint64
	blockCount  uint64
}

// NewValidatorSet creates a validator set with the given config.
func NewValidatorSet(cfg ValidatorSetConfig) *ValidatorSet {
	if cfg.MinStake <= 0 {
		cfg.MinStake = 100.0
	}
	if cfg.MaxValidators <= 0 {
		cfg.MaxValidators = 100
	}
	if cfg.EpochBlocks <= 0 {
		cfg.EpochBlocks = 100
	}
	return &ValidatorSet{
		validators: make(map[string]*Validator),
		config:     cfg,
	}
}

// Register adds a new validator with the given stake.
func (vs *ValidatorSet) Register(address string, stake float64) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if _, exists := vs.validators[address]; exists {
		return ErrAlreadyRegistered
	}
	if stake < vs.config.MinStake {
		return fmt.Errorf("%w: need %.2f, have %.2f", ErrInsufficientStake, vs.config.MinStake, stake)
	}
	if len(vs.validators) >= vs.config.MaxValidators {
		return fmt.Errorf("validator set full (%d/%d)", len(vs.validators), vs.config.MaxValidators)
	}

	vs.validators[address] = &Validator{
		Address:      address,
		Stake:        stake,
		Status:       ValidatorActive,
		RegisteredAt: time.Now(),
	}
	return nil
}

// AddStake increases a validator's stake.
func (vs *ValidatorSet) AddStake(address string, amount float64) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	v, ok := vs.validators[address]
	if !ok {
		return ErrNotRegistered
	}
	if v.Status == ValidatorExited {
		return fmt.Errorf("cannot stake on exited validator")
	}
	v.Stake += amount
	return nil
}

// Exit removes a validator from the active set.
func (vs *ValidatorSet) Exit(address string) (*Validator, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	v, ok := vs.validators[address]
	if !ok {
		return nil, ErrNotRegistered
	}
	v.Status = ValidatorExited
	cp := *v
	return &cp, nil
}

// Slash penalises a validator and jails them.
func (vs *ValidatorSet) Slash(address string, reason SlashReason) (*SlashEvent, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	v, ok := vs.validators[address]
	if !ok {
		return nil, ErrNotRegistered
	}
	if v.Status == ValidatorExited {
		return nil, fmt.Errorf("cannot slash exited validator")
	}

	fraction := vs.config.SlashFraction
	if reason == SlashDowntime {
		fraction = vs.config.DowntimeSlash
	}
	amount := v.Stake * fraction
	v.Stake -= amount
	v.TotalSlashed += amount
	v.SlashCount++
	v.Status = ValidatorJailed
	v.JailedUntil = time.Now().Add(vs.config.JailDuration)

	event := SlashEvent{
		Validator: address,
		Reason:    reason,
		Amount:    amount,
		Epoch:     vs.currentEpoch,
		Timestamp: time.Now(),
	}
	vs.slashLog = append(vs.slashLog, event)
	return &event, nil
}

// Unjail restores a jailed validator if their jail time has elapsed.
func (vs *ValidatorSet) Unjail(address string) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	v, ok := vs.validators[address]
	if !ok {
		return ErrNotRegistered
	}
	if v.Status != ValidatorJailed {
		return fmt.Errorf("validator is not jailed")
	}
	if time.Now().Before(v.JailedUntil) {
		return fmt.Errorf("jail time not elapsed, wait until %s", v.JailedUntil.Format(time.RFC3339))
	}
	if v.Stake < vs.config.MinStake {
		return fmt.Errorf("%w: need %.2f after slashing, have %.2f", ErrInsufficientStake, vs.config.MinStake, v.Stake)
	}
	v.Status = ValidatorActive
	v.JailedUntil = time.Time{}
	return nil
}

// RecordBlock records that a validator produced a block. Advances epoch when threshold is reached.
func (vs *ValidatorSet) RecordBlock(address string) (epochAdvanced bool) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if v, ok := vs.validators[address]; ok {
		v.BlocksProduced++
		v.LastBlockAt = time.Now()
	}
	vs.blockCount++
	if vs.blockCount >= vs.config.EpochBlocks {
		vs.currentEpoch++
		vs.blockCount = 0
		return true
	}
	return false
}

// ActiveValidators returns all non-jailed, non-exited validators sorted by stake descending.
func (vs *ValidatorSet) ActiveValidators() []Validator {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	var active []Validator
	now := time.Now()
	for _, v := range vs.validators {
		if v.Status == ValidatorActive || (v.Status == ValidatorJailed && now.After(v.JailedUntil)) {
			active = append(active, *v)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Stake > active[j].Stake })
	return active
}

// GetValidator returns a copy of a validator's info.
func (vs *ValidatorSet) GetValidator(address string) (*Validator, bool) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	v, ok := vs.validators[address]
	if !ok {
		return nil, false
	}
	cp := *v
	return &cp, true
}

// CurrentEpoch returns the current epoch number.
func (vs *ValidatorSet) CurrentEpoch() uint64 {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.currentEpoch
}

// SlashLog returns all slash events.
func (vs *ValidatorSet) SlashLog() []SlashEvent {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	out := make([]SlashEvent, len(vs.slashLog))
	copy(out, vs.slashLog)
	return out
}

// Size returns the total number of validators (all states).
func (vs *ValidatorSet) Size() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return len(vs.validators)
}

// RegisteredAddresses returns all validator addresses (unordered).
func (vs *ValidatorSet) RegisteredAddresses() []string {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	out := make([]string, 0, len(vs.validators))
	for a := range vs.validators {
		out = append(out, a)
	}
	return out
}

// SetStake sets absolute bonded stake for a known validator (e.g. synced from account balances).
// Values below MinStake are clamped up to MinStake for active/jailed validators.
func (vs *ValidatorSet) SetStake(address string, stake float64) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	v, ok := vs.validators[address]
	if !ok {
		return ErrNotRegistered
	}
	if v.Status == ValidatorExited {
		return fmt.Errorf("cannot set stake on exited validator")
	}
	if stake < vs.config.MinStake {
		stake = vs.config.MinStake
	}
	v.Stake = stake
	return nil
}

// ProposerForEpoch selects the block proposer based on highest stake among active validators.
func (vs *ValidatorSet) ProposerForEpoch() (string, error) {
	active := vs.ActiveValidators()
	if len(active) == 0 {
		return "", fmt.Errorf("no active validators")
	}
	return active[0].Address, nil
}
