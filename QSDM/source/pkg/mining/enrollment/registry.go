package enrollment

// registry.go: adapt an EnrollmentState into the
// hmac.Registry interface that pkg/mining/attest/hmac consumes.
// The adapter is the bridge between consensus-maintained state
// and the attestation-verification hot path.
//
// Split so the on-chain state store can evolve (additional
// indexes, caching strategies, historical queries) without
// forcing hmac.Registry to grow. Registry stays narrow
// (Lookup-only); EnrollmentState may grow as needed.

import (
	"fmt"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

// StateBackedRegistry satisfies hmac.Registry by delegating to
// an EnrollmentState. The wire semantics match
// hmac.InMemoryRegistry exactly:
//
//   - Lookup returns hmac.ErrNodeNotRegistered if the state has
//     no record for the node_id.
//   - Lookup returns hmac.ErrNodeRevoked if the record exists
//     but is in its unbond window (Active() == false).
//   - Lookup returns (entry, nil) on active records.
//
// Safe for concurrent use — all state is held by the underlying
// EnrollmentState which is itself required to be concurrent-safe.
type StateBackedRegistry struct {
	state EnrollmentState
}

// NewStateBackedRegistry builds the adapter. Panics on nil
// state because a nil registry would silently reject every
// enrolled miner and that's the kind of bug that's impossible
// to diagnose from proof-rejection logs.
func NewStateBackedRegistry(state EnrollmentState) *StateBackedRegistry {
	if state == nil {
		panic("enrollment: NewStateBackedRegistry requires non-nil EnrollmentState")
	}
	return &StateBackedRegistry{state: state}
}

// Lookup implements hmac.Registry.
func (r *StateBackedRegistry) Lookup(nodeID string) (*hmac.Entry, error) {
	rec, err := r.state.Lookup(nodeID)
	if err != nil {
		return nil, fmt.Errorf("enrollment: state Lookup: %w", err)
	}
	if rec == nil {
		return nil, hmac.ErrNodeNotRegistered
	}
	if !rec.Active() {
		return nil, hmac.ErrNodeRevoked
	}
	// Defensive copy of HMACKey. The hmac.Entry contract allows
	// callers to mutate what they receive; the underlying
	// EnrollmentRecord is consensus state and must not be
	// touched.
	keyCopy := make([]byte, len(rec.HMACKey))
	copy(keyCopy, rec.HMACKey)
	return &hmac.Entry{
		NodeID:  rec.NodeID,
		GPUUUID: rec.GPUUUID,
		HMACKey: keyCopy,
	}, nil
}

// Compile-time guard that StateBackedRegistry implements
// hmac.Registry.
var _ hmac.Registry = (*StateBackedRegistry)(nil)

// ---------------------------------------------------------------------------
// InMemoryState — test-only EnrollmentState for unit tests and
// local-development networks. NOT for production; there is no
// persistence and no slash coordination.
// ---------------------------------------------------------------------------

// InMemoryState is a minimal thread-safe implementation of
// EnrollmentState. Exposed (rather than kept _test.go-only) so
// downstream callers (e.g. devnet orchestration, integration
// harnesses) can reuse it without re-implementing.
type InMemoryState struct {
	mu          sync.Mutex
	byNodeID    map[string]*EnrollmentRecord
	byGPUActive map[string]string // gpu_uuid -> currently-active node_id
}

// NewInMemoryState returns an empty InMemoryState.
func NewInMemoryState() *InMemoryState {
	return &InMemoryState{
		byNodeID:    make(map[string]*EnrollmentRecord),
		byGPUActive: make(map[string]string),
	}
}

// Lookup implements EnrollmentState.
func (s *InMemoryState) Lookup(nodeID string) (*EnrollmentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byNodeID[nodeID]
	if !ok {
		return nil, nil
	}
	// Return a copy so callers cannot mutate state through the
	// returned pointer. (EnrollmentRecord.HMACKey is a slice;
	// we share the slice header — the HMACKey bytes are read-
	// only by convention in both hmac.Entry and here.)
	cp := *rec
	return &cp, nil
}

// GPUUUIDBound implements EnrollmentState.
func (s *InMemoryState) GPUUUIDBound(gpuUUID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byGPUActive[gpuUUID], nil
}

// ApplyEnroll inserts a new EnrollmentRecord into the state.
// Callers are expected to have run ValidateEnrollAgainstState
// immediately before — ApplyEnroll is the "commit" step that
// only a successful tx should reach. Does not debit any
// balance; the caller's account store is responsible for that.
//
// Returns an error if node_id or gpu_uuid is already bound.
// That's a programmer-error belt (should have been caught by
// validation) rather than an expected-path rejection.
func (s *InMemoryState) ApplyEnroll(rec EnrollmentRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byNodeID[rec.NodeID]; exists {
		return fmt.Errorf("enrollment: InMemoryState.ApplyEnroll: node_id %q already present "+
			"(validation should have caught this)", rec.NodeID)
	}
	if _, bound := s.byGPUActive[rec.GPUUUID]; bound {
		return fmt.Errorf("enrollment: InMemoryState.ApplyEnroll: gpu_uuid %q already bound "+
			"(validation should have caught this)", rec.GPUUUID)
	}
	cp := rec
	s.byNodeID[rec.NodeID] = &cp
	s.byGPUActive[rec.GPUUUID] = rec.NodeID
	return nil
}

// ApplyUnenroll marks the named record as revoked. The record
// remains in state (so the owner's stake stays locked) until
// SweepMaturedUnbonds is called at or after
// UnbondMaturesAtHeight.
func (s *InMemoryState) ApplyUnenroll(nodeID string, currentHeight uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byNodeID[nodeID]
	if !ok {
		return fmt.Errorf("enrollment: InMemoryState.ApplyUnenroll: node_id %q not present",
			nodeID)
	}
	if !rec.Active() {
		return fmt.Errorf("enrollment: InMemoryState.ApplyUnenroll: node_id %q already unenrolled",
			nodeID)
	}
	rec.RevokedAtHeight = currentHeight
	rec.UnbondMaturesAtHeight = currentHeight + UnbondWindow
	// Release the gpu_uuid binding immediately on unenroll so a
	// new node can enroll the same physical GPU without waiting
	// for the unbond to mature. The old record's HMACKey is no
	// longer Active() and therefore cannot be used to mine.
	delete(s.byGPUActive, rec.GPUUUID)
	return nil
}

// SweepMaturedUnbonds deletes revoked records whose
// UnbondMaturesAtHeight ≤ currentHeight and returns the list of
// (owner, stakeDust) pairs that should be credited back. Called
// by the block-time hook (follow-on commit).
func (s *InMemoryState) SweepMaturedUnbonds(currentHeight uint64) []UnbondRelease {
	s.mu.Lock()
	defer s.mu.Unlock()
	var released []UnbondRelease
	for nodeID, rec := range s.byNodeID {
		if rec.MatureForUnbond(currentHeight) {
			released = append(released, UnbondRelease{
				NodeID:    nodeID,
				Owner:     rec.Owner,
				StakeDust: rec.StakeDust,
			})
			delete(s.byNodeID, nodeID)
		}
	}
	return released
}

// UnbondRelease is a single (owner, amount) credit produced by
// SweepMaturedUnbonds. The caller's account store should apply
// each release atomically within the block being sealed.
type UnbondRelease struct {
	NodeID    string
	Owner     string
	StakeDust uint64
}
