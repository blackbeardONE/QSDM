package mining

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sync"
)

// RejectReason is a canonical string identifying why a proof was
// rejected. Dashboards and metrics group by this value, so the set is
// closed — do not introduce new reasons without updating
// MINING_PROTOCOL.md §7 and the audit checklist.
type RejectReason string

const (
	ReasonBadVersion     RejectReason = "bad-version"
	ReasonStaleHeight    RejectReason = "stale-height"
	ReasonWrongEpoch     RejectReason = "wrong-epoch"
	ReasonNonCanonical   RejectReason = "non-canonical"
	ReasonHeaderMismatch RejectReason = "header-mismatch"
	ReasonDuplicate      RejectReason = "duplicate"
	ReasonBadAddr        RejectReason = "bad-addr"
	ReasonBatchSize      RejectReason = "batch-size"
	ReasonBatchRoot      RejectReason = "batch-root"
	ReasonWork           RejectReason = "work"
	ReasonBatchFraud     RejectReason = "batch-fraud"
	ReasonQuarantined    RejectReason = "quarantined"
	ReasonTooLate        RejectReason = "too-late"
)

// RejectError carries a RejectReason plus optional detail. verifier callers
// unwrap it with errors.As to feed metrics.
type RejectError struct {
	Reason RejectReason
	Detail string
}

// Error returns "reason: detail" or just "reason" when detail is empty.
func (e *RejectError) Error() string {
	if e.Detail == "" {
		return string(e.Reason)
	}
	return fmt.Sprintf("%s: %s", e.Reason, e.Detail)
}

// reject is a small constructor that keeps verification sites readable.
func reject(r RejectReason, format string, args ...interface{}) error {
	detail := ""
	if format != "" {
		detail = fmt.Sprintf(format, args...)
	}
	return &RejectError{Reason: r, Detail: detail}
}

// -----------------------------------------------------------------------------
// Dependency interfaces (dependency-injected to keep pkg/mining testable)
// -----------------------------------------------------------------------------

// ChainView is the minimum slice of chain state a verifier needs. The
// reference validator implementation wires pkg/chain into this interface;
// tests supply an in-memory stub.
type ChainView interface {
	// TipHeight returns the height of the current chain tip.
	TipHeight() uint64
	// HeaderHashAt returns the canonical block-header hash for the given
	// height. For heights strictly above the tip or below the first block
	// retained by the node it returns ok=false.
	HeaderHashAt(height uint64) ([32]byte, bool)
}

// AddressValidator decides whether a miner-supplied reward address is
// syntactically valid. The reference validator wires this to
// pkg/crypto's address parser; tests supply a trivial predicate.
type AddressValidator interface {
	ValidateAddress(addr string) error
}

// BatchValidator does the step-11 spot check: given a concrete parent-
// cell batch (looked up from the work-set), is it valid under pkg/mesh3d
// rules? The reference implementation wires pkg/mesh3d; tests use a fake.
type BatchValidator interface {
	ValidateBatch(batch Batch) error
}

// GraceWindow is `G` from MINING_PROTOCOL.md §9: how many blocks past the
// target height a proof may still be accepted for.
const GraceWindow uint64 = 6

// -----------------------------------------------------------------------------
// Dedup set (sliding window of proof IDs)
// -----------------------------------------------------------------------------

// ProofIDSet retains recently-seen proof IDs for the last `retainWindow`
// blocks (MINING_PROTOCOL.md §7 step 6). A new height ticks the window
// forward; IDs older than that are evicted in O(1) amortised.
type ProofIDSet struct {
	mu           sync.Mutex
	retainWindow uint64
	// byHeight keeps the insertion height for each ID so eviction is
	// precise. Tuned for the 60 480-block reference window — a couple of
	// MiB of state even with thousands of proofs per block.
	byHeight map[[32]byte]uint64
}

// NewProofIDSet constructs an empty dedup set that retains proof IDs for
// `retainWindow` blocks.
func NewProofIDSet(retainWindow uint64) *ProofIDSet {
	if retainWindow == 0 {
		retainWindow = DefaultBlocksPerMiningEpoch
	}
	return &ProofIDSet{
		retainWindow: retainWindow,
		byHeight:     make(map[[32]byte]uint64),
	}
}

// Seen reports whether id has been recorded. Safe for concurrent use.
func (s *ProofIDSet) Seen(id [32]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.byHeight[id]
	return ok
}

// Record inserts id at the given block height and evicts stale entries
// relative to that height.
func (s *ProofIDSet) Record(id [32]byte, height uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHeight[id] = height
	if height <= s.retainWindow {
		return
	}
	cutoff := height - s.retainWindow
	for k, v := range s.byHeight {
		if v < cutoff {
			delete(s.byHeight, k)
		}
	}
}

// Size returns the current in-memory size. Exposed for metrics.
func (s *ProofIDSet) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byHeight)
}

// -----------------------------------------------------------------------------
// Quarantine (§8.3)
// -----------------------------------------------------------------------------

// QuarantineSet tracks miner addresses that have submitted a fraudulent
// batch and must sit out for Q blocks.
type QuarantineSet struct {
	mu          sync.Mutex
	quarantinedUntil map[string]uint64
}

// DefaultQuarantineBlocks is `Q` from MINING_PROTOCOL.md §8.3.
const DefaultQuarantineBlocks uint64 = 10_080

// NewQuarantineSet constructs an empty quarantine tracker.
func NewQuarantineSet() *QuarantineSet {
	return &QuarantineSet{quarantinedUntil: make(map[string]uint64)}
}

// Add quarantines addr until `until` (exclusive). If addr is already
// quarantined with a later deadline, the later deadline wins.
func (q *QuarantineSet) Add(addr string, until uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	cur, ok := q.quarantinedUntil[addr]
	if !ok || until > cur {
		q.quarantinedUntil[addr] = until
	}
}

// IsQuarantined reports whether addr is quarantined at the given height.
// Side-effect-free beyond O(1) map lookup.
func (q *QuarantineSet) IsQuarantined(addr string, atHeight uint64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	until, ok := q.quarantinedUntil[addr]
	return ok && atHeight < until
}

// -----------------------------------------------------------------------------
// Verifier
// -----------------------------------------------------------------------------

// VerifierConfig aggregates all the inputs a Verifier needs. Callers
// assemble one per validator process and reuse it across proofs.
type VerifierConfig struct {
	EpochParams    EpochParams
	DifficultyParams DifficultyAdjusterParams

	Chain          ChainView
	Addresses      AddressValidator
	Batches        BatchValidator // may be nil; in that case step 11 is skipped

	Dedup          *ProofIDSet
	Quarantine     *QuarantineSet

	// DAGProvider returns the fully-built DAG for a given mining-epoch.
	// In production this is a weak-references cache of the two most
	// recent epochs (see pkg/mining/dagstore, Phase 4.3 wiring).
	DAGProvider   func(epoch uint64) (DAG, error)

	// WorkSetProvider returns the canonical WorkSet for a given mining-
	// epoch. Both miner and validator MUST agree on this derivation.
	WorkSetProvider func(epoch uint64) (WorkSet, error)

	// DifficultyAt returns the difficulty active at the given block
	// height. Validators keep a per-height difficulty record because the
	// retarget schedule is deterministic.
	DifficultyAt  func(height uint64) (*big.Int, error)

	// GraceWindow overrides the default of 6 blocks. Zero means use the
	// default.
	GraceWindow uint64
}

// Verifier is the stateful acceptance pipeline. It holds no mutable
// state itself — all state is in the injected dependencies — so it is
// safe to share across goroutines.
type Verifier struct {
	cfg VerifierConfig
}

// NewVerifier validates the config and constructs a Verifier.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if err := cfg.EpochParams.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.DifficultyParams.Validate(); err != nil {
		return nil, err
	}
	if cfg.Chain == nil {
		return nil, errors.New("mining: VerifierConfig.Chain is required")
	}
	if cfg.Addresses == nil {
		return nil, errors.New("mining: VerifierConfig.Addresses is required")
	}
	if cfg.Dedup == nil {
		return nil, errors.New("mining: VerifierConfig.Dedup is required")
	}
	if cfg.Quarantine == nil {
		return nil, errors.New("mining: VerifierConfig.Quarantine is required")
	}
	if cfg.DAGProvider == nil {
		return nil, errors.New("mining: VerifierConfig.DAGProvider is required")
	}
	if cfg.WorkSetProvider == nil {
		return nil, errors.New("mining: VerifierConfig.WorkSetProvider is required")
	}
	if cfg.DifficultyAt == nil {
		return nil, errors.New("mining: VerifierConfig.DifficultyAt is required")
	}
	return &Verifier{cfg: cfg}, nil
}

// Verify runs the full §7 acceptance algorithm on a proof given its wire
// bytes. Returns nil for accepted proofs and a *RejectError for rejected
// ones. Callers should metric-log err.(*RejectError).Reason.
//
// On acceptance, the proof is inserted into the dedup set and its ID is
// returned so downstream bookkeeping (coinbase attribution, leaderboard
// updates) can key off it.
//
// `acceptHeight` is the chain height at which the verifier is running.
// Proofs referring to heights more than GraceWindow behind acceptHeight
// are rejected with ReasonTooLate.
func (v *Verifier) Verify(rawProofJSON []byte, acceptHeight uint64) ([32]byte, error) {
	// Step 0: parse.
	p, err := ParseProof(rawProofJSON)
	if err != nil {
		return [32]byte{}, reject(ReasonNonCanonical, "%v", err)
	}

	// Step 4 (moved up to fail fast): canonical-JSON round-trip.
	canonical, err := p.CanonicalJSON()
	if err != nil {
		return [32]byte{}, reject(ReasonNonCanonical, "%v", err)
	}
	if !bytes.Equal(canonical, bytes.TrimSpace(rawProofJSON)) {
		return [32]byte{}, reject(ReasonNonCanonical, "bytes differ from canonical encoding")
	}

	// Step 1: version.
	if p.Version != ProtocolVersion {
		return [32]byte{}, reject(ReasonBadVersion, "got %d want %d", p.Version, ProtocolVersion)
	}

	// Step 2: height within grace window.
	if p.Height > acceptHeight {
		return [32]byte{}, reject(ReasonStaleHeight, "height %d is in the future (tip %d)", p.Height, acceptHeight)
	}
	grace := v.cfg.GraceWindow
	if grace == 0 {
		grace = GraceWindow
	}
	if acceptHeight-p.Height > grace {
		return [32]byte{}, reject(ReasonTooLate, "height %d is %d blocks behind tip %d (grace=%d)",
			p.Height, acceptHeight-p.Height, acceptHeight, grace)
	}

	// Step 3: epoch.
	if p.Epoch != v.cfg.EpochParams.EpochForHeight(p.Height) {
		return [32]byte{}, reject(ReasonWrongEpoch, "proof epoch %d != derived %d",
			p.Epoch, v.cfg.EpochParams.EpochForHeight(p.Height))
	}

	// Step 5: header-hash matches the chain's canonical header at that height.
	canonicalHeader, ok := v.cfg.Chain.HeaderHashAt(p.Height)
	if !ok {
		return [32]byte{}, reject(ReasonHeaderMismatch, "no header at height %d", p.Height)
	}
	if canonicalHeader != p.HeaderHash {
		return [32]byte{}, reject(ReasonHeaderMismatch, "header hash mismatch at height %d", p.Height)
	}

	// Step 6: dedup.
	proofID, err := p.ID()
	if err != nil {
		return [32]byte{}, reject(ReasonNonCanonical, "%v", err)
	}
	if v.cfg.Dedup.Seen(proofID) {
		return proofID, reject(ReasonDuplicate, "proof %x already seen", proofID[:8])
	}

	// Step 7: address sanity.
	if err := v.cfg.Addresses.ValidateAddress(p.MinerAddr); err != nil {
		return proofID, reject(ReasonBadAddr, "%v", err)
	}

	// Step 7a (quarantine): miner sat out for fraud?
	if v.cfg.Quarantine.IsQuarantined(p.MinerAddr, acceptHeight) {
		return proofID, reject(ReasonQuarantined, "miner %s under fraud quarantine", p.MinerAddr)
	}

	// Step 8: batch size bounds. The upper bound is |WS_e|/16 rounded up
	// per spec §7 row 8.
	ws, err := v.cfg.WorkSetProvider(p.Epoch)
	if err != nil {
		return proofID, reject(ReasonBatchSize, "workset lookup: %v", err)
	}
	if err := ws.Validate(); err != nil {
		return proofID, reject(ReasonBatchSize, "workset invalid: %v", err)
	}
	maxBatches := (uint64(len(ws.Batches)) + 15) / 16
	if maxBatches < 1 {
		maxBatches = 1
	}
	if uint64(p.BatchCount) > maxBatches {
		return proofID, reject(ReasonBatchSize, "batch_count %d > max %d", p.BatchCount, maxBatches)
	}
	if p.BatchCount < 1 {
		return proofID, reject(ReasonBatchSize, "batch_count must be >= 1")
	}

	// Step 9: batch-root matches the deterministic prefix-root of the workset.
	wantRoot, err := ws.PrefixRoot(p.BatchCount)
	if err != nil {
		return proofID, reject(ReasonBatchRoot, "prefix-root: %v", err)
	}
	if wantRoot != p.BatchRoot {
		return proofID, reject(ReasonBatchRoot, "batch_root mismatch")
	}

	// Step 10: PoW.
	dag, err := v.cfg.DAGProvider(p.Epoch)
	if err != nil {
		return proofID, reject(ReasonWork, "dag lookup: %v", err)
	}
	mix, err := ComputeMixDigest(p.HeaderHash, p.Nonce, dag)
	if err != nil {
		return proofID, reject(ReasonWork, "mix: %v", err)
	}
	if mix != p.MixDigest {
		return proofID, reject(ReasonWork, "mix_digest mismatch")
	}
	diff, err := v.cfg.DifficultyAt(p.Height)
	if err != nil {
		return proofID, reject(ReasonWork, "difficulty lookup: %v", err)
	}
	tgt, err := TargetFromDifficulty(diff)
	if err != nil {
		return proofID, reject(ReasonWork, "target derive: %v", err)
	}
	h := ProofPoWHash(p.HeaderHash, p.Nonce, p.BatchRoot, p.MixDigest)
	if !MeetsTarget(h, tgt) {
		return proofID, reject(ReasonWork, "hash does not meet target")
	}

	// Step 11: probabilistic spot-check. Deterministic choice of leaf
	// index so replaying the verification from the same proof always
	// picks the same batch — turns the spot-check into a reproducible
	// artefact auditors can re-run.
	if v.cfg.Batches != nil {
		leafIdx := deterministicSpotIndex(proofID, p.BatchCount)
		if leafIdx < uint32(len(ws.Batches)) {
			if err := v.cfg.Batches.ValidateBatch(ws.Batches[leafIdx]); err != nil {
				// Slash per §8.3: quarantine the miner address. The actual
				// quarantine-until height is set by the caller because it
				// depends on the current tip.
				q := acceptHeight + DefaultQuarantineBlocks
				v.cfg.Quarantine.Add(p.MinerAddr, q)
				return proofID, reject(ReasonBatchFraud, "leaf %d failed: %v", leafIdx, err)
			}
		}
	}

	// All checks passed. Record dedup and return.
	v.cfg.Dedup.Record(proofID, p.Height)
	return proofID, nil
}

// deterministicSpotIndex picks a leaf index into the batch in a way that
// depends only on the proof ID, so the choice is reproducible by any
// auditor replaying the verification.
func deterministicSpotIndex(proofID [32]byte, batchCount uint32) uint32 {
	if batchCount == 0 {
		return 0
	}
	seed := binary.BigEndian.Uint32(proofID[:4])
	return seed % batchCount
}
