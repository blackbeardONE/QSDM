package chainparams

// store.go ships the ParamStore interface and its in-memory
// reference implementation, InMemoryParamStore.
//
// # The interface
//
// ParamStore is the consensus-relevant state that:
//   - Holds the active value for every registered parameter
//     (initialised from ParamSpec.DefaultValue at construction).
//   - Stages pending changes scheduled at a future EffectiveHeight.
//   - Promotes pending → active when the chain advances past
//     each change's EffectiveHeight.
//   - Reports the active value to readers (chain.SlashApplier,
//     monitoring gauges, the HTTP API).
//
// The interface is small and read-mostly; chain.SlashApplier
// is the only hot-path reader, calling RewardBPS() and
// AutoRevokeMinStakeDust() once per slash tx.
//
// # Concurrency
//
// All methods are safe for concurrent use. Reads (ActiveValue,
// Pending) take an RWMutex's read lock; writes (Stage, Promote,
// SetForTesting) take the write lock.

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ParamStore is the read+write surface required by the
// chain-side consensus layer.
//
// IMPORTANT: implementations MUST persist the active and
// pending state across node restarts. The in-memory reference
// implementation here does NOT persist (that's a node-side
// responsibility, the same way enrollment.InMemoryState is
// non-persistent and the persistence layer wraps it).
type ParamStore interface {
	// ActiveValue returns the currently-active value for the
	// named parameter. Returns the registry default if no
	// governance change has yet been activated. Returns
	// (0, false) if the parameter is not in the registry —
	// callers MUST treat that as a programmer error
	// (registry mismatch between binary and store).
	ActiveValue(name string) (uint64, bool)

	// Pending returns the pending change for the named
	// parameter, or (zero, false) if no change is scheduled.
	Pending(name string) (ParamChange, bool)

	// AllActive returns a snapshot of the active value for
	// every registered parameter. Used by the metrics layer
	// to repopulate gauges and by the HTTP API for
	// `/api/v1/governance/params`.
	AllActive() map[string]uint64

	// AllPending returns a snapshot of every pending change.
	// Order: by EffectiveHeight ascending, then param name
	// ascending — deterministic so two callers see the same
	// list.
	AllPending() []ParamChange

	// Stage records a new pending change. Validates the change
	// against the registry (unknown name → ErrUnknownParam,
	// out-of-bounds → ErrValueOutOfBounds). On success any
	// existing pending change for the same parameter is
	// REPLACED (the spec's "one pending per param,
	// supersedable" rule).
	//
	// Returns the prior pending change (if any) so the
	// applier can publish a "supersede" event.
	Stage(change ParamChange) (prior ParamChange, hadPrior bool, err error)

	// Promote walks every pending change and promotes any
	// whose EffectiveHeight <= currentHeight to active,
	// returning the list of promoted changes in promotion
	// order. Idempotent: calling twice with the same
	// currentHeight is a no-op on the second call.
	//
	// Called from the SealedBlockHook after each block.
	Promote(currentHeight uint64) []ParamChange
}

// InMemoryParamStore is the reference implementation. Held
// behind a single sync.RWMutex; the store is small (≤ a dozen
// parameters) and contention is low (writes only on apply, in
// the chain's serial apply path).
type InMemoryParamStore struct {
	mu      sync.RWMutex
	active  map[string]uint64
	pending map[string]ParamChange
}

// NewInMemoryParamStore constructs a store with every
// registered parameter initialised to its registry default.
// The returned store is safe to share with the chain applier
// AND with monitoring readers concurrently.
func NewInMemoryParamStore() *InMemoryParamStore {
	s := &InMemoryParamStore{
		active:  make(map[string]uint64, len(registry)),
		pending: make(map[string]ParamChange),
	}
	for _, spec := range registry {
		s.active[string(spec.Name)] = spec.DefaultValue
	}
	return s
}

// SetForTesting overrides the active value for a parameter.
// PANICS if the parameter is unknown or the value is out of
// bounds — this is a test helper and the panic is the bug
// signal. NEVER call from production code; use Stage + Promote.
func (s *InMemoryParamStore) SetForTesting(name string, value uint64) {
	spec, ok := Lookup(name)
	if !ok {
		panic(fmt.Sprintf(
			"chainparams: SetForTesting unknown param %q (registry: %s)",
			name, formatNames()))
	}
	if err := spec.CheckBounds(value); err != nil {
		panic(fmt.Sprintf("chainparams: SetForTesting %q: %v", name, err))
	}
	s.mu.Lock()
	s.active[name] = value
	s.mu.Unlock()
}

// ActiveValue implements ParamStore.
func (s *InMemoryParamStore) ActiveValue(name string) (uint64, bool) {
	if _, ok := Lookup(name); !ok {
		return 0, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.active[name]
	return v, ok
}

// Pending implements ParamStore.
func (s *InMemoryParamStore) Pending(name string) (ParamChange, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.pending[name]
	return c, ok
}

// AllActive implements ParamStore. Returns a fresh map so
// callers can't mutate the store via the returned value.
func (s *InMemoryParamStore) AllActive() map[string]uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]uint64, len(s.active))
	for k, v := range s.active {
		out[k] = v
	}
	return out
}

// AllPending implements ParamStore. Returns a deterministic
// ordering: by EffectiveHeight ascending, then by name
// ascending.
func (s *InMemoryParamStore) AllPending() []ParamChange {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ParamChange, 0, len(s.pending))
	for _, c := range s.pending {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EffectiveHeight != out[j].EffectiveHeight {
			return out[i].EffectiveHeight < out[j].EffectiveHeight
		}
		return out[i].Param < out[j].Param
	})
	return out
}

// Stage implements ParamStore.
func (s *InMemoryParamStore) Stage(change ParamChange) (ParamChange, bool, error) {
	spec, ok := Lookup(change.Param)
	if !ok {
		return ParamChange{}, false, fmt.Errorf(
			"%w: param=%q (registry: %s)",
			ErrUnknownParam, change.Param, formatNames())
	}
	if err := spec.CheckBounds(change.Value); err != nil {
		return ParamChange{}, false, err
	}
	if change.EffectiveHeight == 0 {
		return ParamChange{}, false, errors.New(
			"chainparams: Stage requires non-zero EffectiveHeight")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	prior, hadPrior := s.pending[change.Param]
	s.pending[change.Param] = change
	return prior, hadPrior, nil
}

// Promote implements ParamStore. Promotion order is by
// EffectiveHeight ascending; ties broken by param-name
// ascending to keep the output deterministic across nodes.
func (s *InMemoryParamStore) Promote(currentHeight uint64) []ParamChange {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Snapshot of names + heights so we can sort before mutating.
	type ent struct {
		name string
		c    ParamChange
	}
	var due []ent
	for name, c := range s.pending {
		// Effective height comparison includes the configured
		// reorg-grace; today DefaultPromotionGrace is 0 so
		// the comparison is exact.
		if c.EffectiveHeight+DefaultPromotionGrace <= currentHeight {
			due = append(due, ent{name: name, c: c})
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].c.EffectiveHeight != due[j].c.EffectiveHeight {
			return due[i].c.EffectiveHeight < due[j].c.EffectiveHeight
		}
		return due[i].name < due[j].name
	})

	promoted := make([]ParamChange, 0, len(due))
	for _, e := range due {
		s.active[e.name] = e.c.Value
		delete(s.pending, e.name)
		promoted = append(promoted, e.c)
	}
	return promoted
}

// Compile-time assertion.
var _ ParamStore = (*InMemoryParamStore)(nil)
