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
//
// Also carries the slashing replay-protection set
// (seenEvidence) and supports stake forfeiture via SlashStake.
// Both are part of the in-memory state because slashing
// transitions need the same lock as enroll/unenroll to keep
// SlashStake atomic with the rest of the record-mutating ops.
type InMemoryState struct {
	mu            sync.Mutex
	byNodeID      map[string]*EnrollmentRecord
	byGPUActive   map[string]string  // gpu_uuid -> currently-active node_id
	seenEvidence  map[[32]byte]bool  // dedup key for slash evidence (replay protection)
}

// NewInMemoryState returns an empty InMemoryState.
func NewInMemoryState() *InMemoryState {
	return &InMemoryState{
		byNodeID:     make(map[string]*EnrollmentRecord),
		byGPUActive:  make(map[string]string),
		seenEvidence: make(map[[32]byte]bool),
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

// SlashStake reduces the StakeDust of the named EnrollmentRecord
// by min(amount, record.StakeDust) and returns the actually-
// forfeited amount. Returns (0, error) if the record does not
// exist; (0, nil) is the legitimate "record exists but already
// has zero remaining stake" outcome.
//
// Does NOT touch the GPU UUID binding or the active flag —
// slashing reduces the bond but does not auto-revoke. A miner
// whose stake has been fully drained remains "enrolled" until
// they unenroll or the chain re-validates the bond against the
// current minimum at the next epoch (out of scope for this
// commit). This keeps slashing orthogonal to the unenroll/
// sweep flow.
//
// Implements the contract relied on by pkg/chain.SlashApplier.
func (s *InMemoryState) SlashStake(nodeID string, amount uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byNodeID[nodeID]
	if !ok {
		return 0, fmt.Errorf("enrollment: SlashStake: node_id %q not present", nodeID)
	}
	slashed := amount
	if slashed > rec.StakeDust {
		slashed = rec.StakeDust
	}
	rec.StakeDust -= slashed
	return slashed, nil
}

// MarkEvidenceSeen adds `hash` to the seen-evidence set and
// returns true if the hash was newly inserted, false if it had
// already been seen. Used by the slashing applier to enforce
// "one slash per evidence hash" — without this, the same proof
// of misbehaviour could be replayed by a thousand peers and
// drain a miner's stake N× times for one offence.
//
// Hash key is opaque to the state — the SlashApplier picks the
// hashing scheme. Today: SHA-256(EvidenceKind || EvidenceBlob).
//
// Concurrency: same mutex as the rest of the state, so
// MarkEvidenceSeen + SlashStake within the same applier path
// MUST be issued in that order from the SAME goroutine for
// atomicity. Two concurrent slashers on the same evidence will
// see exactly one of them succeed via the bool return.
func (s *InMemoryState) MarkEvidenceSeen(hash [32]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seenEvidence[hash] {
		return false
	}
	s.seenEvidence[hash] = true
	return true
}

// EvidenceSeen reports whether the given hash has already been
// recorded by MarkEvidenceSeen. Read-only — does not mutate the
// set. Useful for diagnostics and pre-flight checks.
func (s *InMemoryState) EvidenceSeen(hash [32]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seenEvidence[hash]
}

// CloneableState is the optional extension EnrollmentState
// implementations can satisfy to support speculative replay
// (pre-seal BFT, TryAppendExternalBlock). The interface lives
// in this package so concrete types can implement it without
// pulling in pkg/chain (which would form an import cycle).
//
// Implementers must ensure:
//
//   - Clone() returns a fully independent snapshot. Mutations
//     to the snapshot must NOT be visible on the receiver and
//     vice versa.
//   - Restore(from) overwrites the receiver atomically with the
//     contents of `from`. Errors on type mismatch.
type CloneableState interface {
	Clone() CloneableState
	Restore(from CloneableState) error
}

// Clone returns a deep copy of the InMemoryState. Implements
// CloneableState — used by ChainReplayApplier-style speculative
// replay (pre-seal BFT, TryAppendExternalBlock). The clone
// receives ApplyEnroll / ApplyUnenroll mutations against the
// same on-chain semantics as the live state but without touching
// it. The caller may discard the clone to abandon the
// speculative work, or promote it via Restore.
//
// Concurrency note: Clone snapshots under the same mutex that
// guards mutations, so a caller racing with an ApplyEnroll
// will see either the pre- or post-mutation state but never a
// torn map.
func (s *InMemoryState) Clone() CloneableState {
	if s == nil {
		return NewInMemoryState()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := &InMemoryState{
		byNodeID:     make(map[string]*EnrollmentRecord, len(s.byNodeID)),
		byGPUActive:  make(map[string]string, len(s.byGPUActive)),
		seenEvidence: make(map[[32]byte]bool, len(s.seenEvidence)),
	}
	for k, rec := range s.byNodeID {
		// Deep-copy each record. EnrollmentRecord.HMACKey is a
		// slice; failing to copy it would let the clone share
		// the byte buffer with the live state and any later
		// mutation in the live state (e.g. a re-enroll that
		// overwrites the same node_id) would leak into the
		// snapshot.
		dup := *rec
		if rec.HMACKey != nil {
			dup.HMACKey = append([]byte(nil), rec.HMACKey...)
		}
		cp.byNodeID[k] = &dup
	}
	for k, v := range s.byGPUActive {
		cp.byGPUActive[k] = v
	}
	for k := range s.seenEvidence {
		cp.seenEvidence[k] = true
	}
	return cp
}

// Restore replaces the receiver's contents with a snapshot
// produced by Clone. Implements CloneableState. Used as the
// rollback step when speculative replay fails
// (TryAppendExternalBlock state-root mismatch, pre-seal BFT
// abort). The replacement is atomic under the receiver's lock,
// so concurrent readers see a consistent map state at all times.
//
// Returns an error if `from` is nil or the wrong concrete type
// — Restore semantics require an explicit, type-matched
// snapshot, never a silent reset to empty.
func (s *InMemoryState) Restore(from CloneableState) error {
	if s == nil {
		return fmt.Errorf("enrollment: Restore on nil InMemoryState")
	}
	if from == nil {
		return fmt.Errorf("enrollment: Restore requires non-nil source")
	}
	src, ok := from.(*InMemoryState)
	if !ok {
		return fmt.Errorf("enrollment: Restore expects *InMemoryState snapshot, got %T", from)
	}
	src.mu.Lock()
	srcByNodeID := make(map[string]*EnrollmentRecord, len(src.byNodeID))
	srcByGPUActive := make(map[string]string, len(src.byGPUActive))
	srcSeen := make(map[[32]byte]bool, len(src.seenEvidence))
	for k, rec := range src.byNodeID {
		dup := *rec
		if rec.HMACKey != nil {
			dup.HMACKey = append([]byte(nil), rec.HMACKey...)
		}
		srcByNodeID[k] = &dup
	}
	for k, v := range src.byGPUActive {
		srcByGPUActive[k] = v
	}
	for k := range src.seenEvidence {
		srcSeen[k] = true
	}
	src.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.byNodeID = srcByNodeID
	s.byGPUActive = srcByGPUActive
	s.seenEvidence = srcSeen
	return nil
}

// Compile-time guard that *InMemoryState satisfies CloneableState.
var _ CloneableState = (*InMemoryState)(nil)
