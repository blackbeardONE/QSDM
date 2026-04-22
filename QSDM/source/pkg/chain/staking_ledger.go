package chain

import (
	"fmt"
	"sync"
)

// StakingLedger holds delegated voting power, per-delegator bonds, and unbonding queues.
type StakingLedger struct {
	mu             sync.RWMutex
	delegated      map[string]float64             // validator -> total delegated (denormalized)
	delegatorIndex map[string]map[string]float64 // delegator -> validator -> amount
	unbond         []unbondEntry
	persistPath    string
}

type unbondEntry struct {
	Delegator string  `json:"delegator"`
	Amount    float64 `json:"amount"`
	MatureAt  uint64  `json:"mature_at"`
}

// NewStakingLedger creates an empty staking ledger.
func NewStakingLedger() *StakingLedger {
	return &StakingLedger{
		delegated:      make(map[string]float64),
		delegatorIndex: make(map[string]map[string]float64),
	}
}

// SetPersistPath sets the JSON file path used by persist(); empty disables disk writes.
func (s *StakingLedger) SetPersistPath(path string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.persistPath = path
	s.mu.Unlock()
}

func (s *StakingLedger) persist() {
	if s == nil {
		return
	}
	s.mu.RLock()
	p := s.persistPath
	s.mu.RUnlock()
	if p == "" {
		return
	}
	_ = SaveStakingLedger(s, p)
}

// DelegatedPower returns bonded weight for a validator (0 if none).
func (s *StakingLedger) DelegatedPower(validator string) float64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.delegated[validator]
}

// Bonded returns the amount a delegator has bonded to a validator.
func (s *StakingLedger) Bonded(delegator, validator string) float64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if m, ok := s.delegatorIndex[delegator]; ok {
		return m[validator]
	}
	return 0
}

// Delegate moves balance from delegator to validator voting power (bonded).
func (s *StakingLedger) Delegate(as *AccountStore, delegator, validator string, amount float64) error {
	if s == nil {
		return fmt.Errorf("nil staking ledger")
	}
	if amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if err := as.Debit(delegator, amount); err != nil {
		return err
	}
	s.mu.Lock()
	if s.delegatorIndex[delegator] == nil {
		s.delegatorIndex[delegator] = make(map[string]float64)
	}
	s.delegatorIndex[delegator][validator] += amount
	s.delegated[validator] += amount
	s.mu.Unlock()
	s.persist()
	return nil
}

// BeginUnbond schedules return of delegated power to the delegator at MatureAt height.
// Voting power drops immediately; funds credit on ProcessCommittedHeight.
func (s *StakingLedger) BeginUnbond(as *AccountStore, delegator, validator string, amount float64, currentHeight, unbondBlocks uint64) error {
	if s == nil {
		return fmt.Errorf("nil staking ledger")
	}
	if amount <= 0 || unbondBlocks == 0 {
		return fmt.Errorf("invalid unbond")
	}
	s.mu.Lock()
	m, ok := s.delegatorIndex[delegator]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("no bonds for delegator %s", delegator)
	}
	have := m[validator]
	if have < amount {
		s.mu.Unlock()
		return fmt.Errorf("unbond exceeds bond for %s -> %s", delegator, validator)
	}
	m[validator] -= amount
	if m[validator] < 1e-12 {
		delete(m, validator)
	}
	if len(m) == 0 {
		delete(s.delegatorIndex, delegator)
	}
	s.delegated[validator] -= amount
	if s.delegated[validator] < 1e-12 {
		delete(s.delegated, validator)
	}
	mature := currentHeight + unbondBlocks
	s.unbond = append(s.unbond, unbondEntry{Delegator: delegator, Amount: amount, MatureAt: mature})
	s.mu.Unlock()
	s.persist()
	return nil
}

// SlashDelegated scales down all bonds to a validator (e.g. evidence hook).
func (s *StakingLedger) SlashDelegated(validator string, slashFraction float64) {
	if s == nil || slashFraction <= 0 || slashFraction > 1 {
		return
	}
	s.mu.Lock()
	f := 1.0 - slashFraction
	delegators := make([]string, 0, len(s.delegatorIndex))
	for d := range s.delegatorIndex {
		delegators = append(delegators, d)
	}
	for _, del := range delegators {
		m := s.delegatorIndex[del]
		if m == nil {
			continue
		}
		if amt, ok := m[validator]; ok && amt > 0 {
			m[validator] = amt * f
			if m[validator] < 1e-12 {
				delete(m, validator)
			}
			if len(m) == 0 {
				delete(s.delegatorIndex, del)
			}
		}
	}
	if p, ok := s.delegated[validator]; ok {
		s.delegated[validator] = p * f
		if s.delegated[validator] < 1e-12 {
			delete(s.delegated, validator)
		}
	}
	s.mu.Unlock()
	s.persist()
}

// ProcessCommittedHeight matures unbonding entries and credits accounts (stateRoot reserved for audits).
func (s *StakingLedger) ProcessCommittedHeight(as *AccountStore, height uint64, stateRoot string) {
	if s == nil || as == nil {
		return
	}
	_ = stateRoot
	s.mu.Lock()
	kept := s.unbond[:0]
	for _, e := range s.unbond {
		if e.MatureAt <= height {
			as.Credit(e.Delegator, e.Amount)
			continue
		}
		kept = append(kept, e)
	}
	s.unbond = kept
	s.mu.Unlock()
	s.persist()
}

func applyStakingDelegationWeights(vs *ValidatorSet, sl *StakingLedger) {
	if vs == nil || sl == nil {
		return
	}
	sl.mu.RLock()
	defer sl.mu.RUnlock()
	for val, pow := range sl.delegated {
		if pow <= 0 {
			continue
		}
		v, ok := vs.GetValidator(val)
		if !ok {
			continue
		}
		_ = vs.SetStake(val, v.Stake+pow)
	}
}
