// Package miningsvc constructs the boot-time concrete
// api.MiningService that the validator binary installs via
// api.SetMiningService at startup. It is the bridge between
// chain state (BlockProducer, account store) and the
// pkg/mining acceptance pipeline (Verifier, DAG, WorkSet).
//
// Until this package shipped (Phase 2c-v wiring), the
// validator's /api/v1/mining/work and /api/v1/mining/submit
// routes returned 503 Service Unavailable on every request:
// the HTTP layer was wired but no MiningService had ever
// been installed. The pre-2026-05-06 BLR1 production binary
// reflected exactly that posture — the live curl probe
// captured `{"error":"mining_unavailable"}` from the public
// endpoint.
//
// Scope of this package:
//
//   - Implements api.MiningService (WorkAt, Submit,
//     TipHeight) against a *chain.BlockProducer and a
//     pre-built *mining.Verifier. Stateless aside from the
//     verifier's internal Dedup/Quarantine sets and our DAG
//     cache.
//
//   - Owns a small DAG cache (default cap = 2 epochs) so
//     repeated Submit calls within a mining-epoch don't
//     rebuild the DAG every time. ProductionDAGSize is 2 GiB,
//     but the bring-up posture uses a smaller dag_size
//     (operator-tunable) so a fresh testnet validator stays
//     under a few MiB.
//
//   - Wires mining.NewVerifier with all 9 required
//     collaborators. Several are pure constructors
//     (NewProofIDSet, NewQuarantineSet, NewEpochParams,
//     NewDifficultyAdjusterParams); the chain-touching ones
//     (Chain, DAGProvider, WorkSetProvider, DifficultyAt) all
//     route back through this Service.
//
// Out of scope:
//
//   - Mining-epoch–scoped WorkSet derivation from chain
//     state (i.e. "the WorkSet for epoch E is the set of
//     parent cells frozen at the epoch boundary"). The
//     bring-up posture is to take a single deterministic
//     WorkSet from cfg, canonicalise it, and serve it for
//     every height. The full §3.2 epoch-boundary derivation
//     is a follow-on (Phase 4.6 work) that doesn't block
//     today's "miner can produce a proof, validator
//     accepts it" deliverable.
//
//   - Difficulty retargeting from chain history. cfg supplies
//     a constant difficulty; production retargeting reads
//     prior block timestamps and uses
//     mining.DifficultyAdjuster, but that's a chain-storage
//     plumbing job orthogonal to enabling mining.
//
//   - v2 attestation. Pre-fork proofs (Height < ForkV2Height)
//     skip the Attestation hook entirely per
//     pkg/mining/verifier.go's documented contract. The
//     bring-up posture leaves Attestation nil, and the
//     verifier's auto-injected FailClosedVerifier handles the
//     post-fork case (rejecting v2 proofs until cmd/qsdm
//     wires attest.NewProductionDispatcher — Phase 2c-vii
//     work).
package miningsvc

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// Config bundles collaborators the Service needs. The zero
// value is INVALID — New returns an error rather than
// silently defaulting any consensus-touching field. Config
// is intentionally narrow: only the fields the service
// itself reads. Bigger plumbing surfaces (the producer's
// applier, the mempool, etc.) come in via the producer.
type Config struct {
	// Producer is the live block producer the service queries
	// for tip height and historical block headers. REQUIRED.
	// The producer's GetBlock and TipHeight are called from
	// any goroutine via the HTTP handler; both are
	// concurrency-safe in pkg/chain.
	Producer *chain.BlockProducer

	// WorkSet is the deterministic parent-cell set served to
	// every miner. REQUIRED. New canonicalises a defensive
	// copy so the caller's slice is not mutated. The miner
	// rebuilds its DAG locally from ws.Root() so any
	// canonicalisation must happen here, not on the wire.
	//
	// Bring-up posture: a single static WorkSet good for the
	// entire chain lifetime. Phase 4.6 work will replace this
	// with a per-epoch derivation rooted in chain state.
	WorkSet mining.WorkSet

	// DAGSize is the value of N served in MiningWork.dag_size
	// and used by the verifier when reconstructing the DAG to
	// validate a proof's mix_digest. REQUIRED. Must be >= 2;
	// production mainnet uses mining.ProductionDAGSize
	// (2^26 = 64 MiB entries → 2 GiB resident). Bring-up
	// testnet uses something tiny (1024–65536) so the
	// validator's RAM and the miner's DAG-build time both
	// stay reasonable.
	DAGSize uint32

	// Difficulty is the static target difficulty served to
	// every miner. REQUIRED, must be > 0. Production mainnet
	// will replace this with a chain-state-driven retarget
	// (mining.DifficultyAdjuster + per-block timestamps);
	// bring-up posture uses a constant.
	Difficulty *big.Int

	// BlocksPerEpoch overrides the default mining-epoch length.
	// Zero uses mining.DefaultBlocksPerMiningEpoch. The miner's
	// MiningWork.blocks_per_epoch field carries this so the
	// miner derives the same epoch from a height as the
	// validator does.
	BlocksPerEpoch uint64

	// Addresses is the address validator the verifier uses on
	// the proof's miner_addr field. Optional: defaults to a
	// non-empty-string check, which is appropriate for
	// bring-up but should be replaced with the real wallet
	// address parser once the chain has a stable address
	// format.
	Addresses mining.AddressValidator

	// Batches is the structural-batch validator. Optional:
	// defaults to accept-all. The static cfg.WorkSet is the
	// authoritative shape for the bring-up posture; tighter
	// structural rules (per-batch min/max cell counts, etc.)
	// live in the WorkSet derivation that replaces this.
	Batches mining.BatchValidator

	// DedupCapacity is the retention window (in blocks) the
	// verifier's ProofIDSet keeps before evicting old proof
	// IDs. Zero uses 4096 — a few hours of legitimate miner
	// traffic at testnet block rates.
	DedupCapacity uint64
}

// Service is the concrete api.MiningService the validator
// installs at startup. Stateless from a consensus point of
// view — all consensus state lives in pkg/chain — but holds
// a small, bounded DAG cache and the wrapped *mining.Verifier
// so successive Submit calls don't rebuild every time.
//
// Safe for concurrent use: WorkAt, Submit, and TipHeight may
// all run from any goroutine. The DAG cache uses RWMutex; the
// verifier itself is documented as concurrent-safe.
type Service struct {
	producer       *chain.BlockProducer
	ws             mining.WorkSet
	dagSize        uint32
	difficulty     *big.Int // owned copy; never mutated
	blocksPerEpoch uint64

	dagMu    sync.RWMutex
	dagCache map[uint64]mining.DAG // keyed by epoch; cap = 2

	verifier *mining.Verifier
}

// dagCacheCap bounds the DAG map's resident size. Two is the
// minimum that lets a miner straddle an epoch boundary
// without triggering a rebuild on every alternating Submit.
// Larger caps would only matter for an operator that sees
// consistent traffic across many epochs simultaneously, which
// is not the bring-up posture.
const dagCacheCap = 2

// New validates cfg and returns a ready-to-install Service.
// Returns an error if any required collaborator is missing
// or a structural invariant is violated (DAGSize < 2,
// Difficulty <= 0, etc.).
func New(cfg Config) (*Service, error) {
	if cfg.Producer == nil {
		return nil, errors.New("miningsvc: Config.Producer is required")
	}
	if cfg.DAGSize < 2 {
		return nil, fmt.Errorf("miningsvc: Config.DAGSize=%d must be >= 2", cfg.DAGSize)
	}
	if cfg.Difficulty == nil || cfg.Difficulty.Sign() <= 0 {
		return nil, errors.New("miningsvc: Config.Difficulty must be > 0")
	}
	if len(cfg.WorkSet.Batches) == 0 {
		return nil, errors.New("miningsvc: Config.WorkSet must contain at least one batch")
	}
	// Defensive copy + canonicalise so the caller's slice is
	// untouched and the served WorkSet matches what the verifier
	// expects on Submit.
	ws := cloneWorkSet(cfg.WorkSet)
	ws.Canonicalize()
	if err := ws.Validate(); err != nil {
		return nil, fmt.Errorf("miningsvc: WorkSet.Validate: %w", err)
	}

	bpe := cfg.BlocksPerEpoch
	if bpe == 0 {
		bpe = mining.DefaultBlocksPerMiningEpoch
	}

	addrs := cfg.Addresses
	if addrs == nil {
		addrs = nonEmptyAddressValidator{}
	}
	batches := cfg.Batches
	if batches == nil {
		batches = acceptAllBatchValidator{}
	}
	var dedupCap uint64
	if cfg.DedupCapacity > 0 {
		dedupCap = uint64(cfg.DedupCapacity)
	} else {
		dedupCap = 4096
	}

	svc := &Service{
		producer:       cfg.Producer,
		ws:             ws,
		dagSize:        cfg.DAGSize,
		difficulty:     new(big.Int).Set(cfg.Difficulty),
		blocksPerEpoch: bpe,
		dagCache:       make(map[uint64]mining.DAG, dagCacheCap),
	}

	v, err := mining.NewVerifier(mining.VerifierConfig{
		EpochParams:      mining.EpochParams{BlocksPerEpoch: bpe},
		DifficultyParams: mining.NewDifficultyAdjusterParams(),
		Chain:            chainAdapter{producer: cfg.Producer},
		Addresses:        addrs,
		Batches:          batches,
		Dedup:            mining.NewProofIDSet(dedupCap),
		Quarantine:       mining.NewQuarantineSet(),
		DAGProvider:      svc.dagFor,
		WorkSetProvider:  svc.workSetFor,
		DifficultyAt:     svc.difficultyFor,
	})
	if err != nil {
		return nil, fmt.Errorf("miningsvc: build verifier: %w", err)
	}
	svc.verifier = v
	return svc, nil
}

// WorkAt implements api.MiningService. Returns the work
// payload for the requested height, clamped down to the
// current chain tip when the caller asked for a future block
// (the HTTP handler defaults the no-param path to tip+1, but
// the miner can only mine on already-sealed blocks because
// the verifier's step 5 cross-checks header_hash against the
// chain).
//
// Returns api.ErrMiningUnavailable when the chain has no
// blocks (genesis-only) or when the requested height has no
// block on file.
func (s *Service) WorkAt(height uint64) (*api.MiningWork, error) {
	if !s.producer.HasTip() {
		return nil, api.ErrMiningUnavailable
	}
	tip := s.producer.TipHeight()
	if height > tip {
		// The HTTP handler's default ("no ?height=") arrives here
		// as tip+1; clamp down to tip so the miner mines on the
		// most-recent sealed block rather than getting a 503.
		// Explicit lower heights (including 0 = genesis) are
		// honoured as-is.
		height = tip
	}
	block, ok := s.producer.GetBlock(height)
	if !ok {
		return nil, api.ErrMiningUnavailable
	}
	var hdr [32]byte
	if err := decodeHexInto(hdr[:], block.Hash); err != nil {
		return nil, fmt.Errorf("miningsvc: block %d hash decode: %w", height, err)
	}
	epoch := height / s.blocksPerEpoch
	work, err := api.WorkFromMiningCore(
		epoch, height, hdr,
		new(big.Int).Set(s.difficulty),
		s.dagSize, s.ws, s.blocksPerEpoch,
	)
	if err != nil {
		return nil, fmt.Errorf("miningsvc: build work payload: %w", err)
	}
	return work, nil
}

// Submit implements api.MiningService. Forwards to the
// underlying verifier with the current tip as the
// acceptHeight. Rejection reasons are the standard
// pkg/mining sentinels and propagate to the HTTP layer
// untouched.
func (s *Service) Submit(rawProofJSON []byte) ([32]byte, error) {
	tip := s.producer.TipHeight()
	return s.verifier.Verify(rawProofJSON, tip)
}

// TipHeight implements api.MiningService.
func (s *Service) TipHeight() uint64 { return s.producer.TipHeight() }

// dagFor returns the DAG for a mining-epoch, building and
// caching on first request. The cache is bounded at
// dagCacheCap entries; eviction picks an arbitrary key to
// drop, which is fine because the caller always pays the
// same per-epoch build cost regardless of which entry got
// evicted.
func (s *Service) dagFor(epoch uint64) (mining.DAG, error) {
	s.dagMu.RLock()
	if dag, ok := s.dagCache[epoch]; ok {
		s.dagMu.RUnlock()
		return dag, nil
	}
	s.dagMu.RUnlock()

	s.dagMu.Lock()
	defer s.dagMu.Unlock()
	if dag, ok := s.dagCache[epoch]; ok {
		return dag, nil
	}
	dag, err := mining.NewInMemoryDAG(epoch, s.ws.Root(), s.dagSize)
	if err != nil {
		return nil, fmt.Errorf("miningsvc: build DAG epoch=%d: %w", epoch, err)
	}
	for len(s.dagCache) >= dagCacheCap {
		for e := range s.dagCache {
			delete(s.dagCache, e)
			break
		}
	}
	s.dagCache[epoch] = dag
	return dag, nil
}

// workSetFor returns the static WorkSet. Bring-up posture;
// see package comment for the planned upgrade.
func (s *Service) workSetFor(_ uint64) (mining.WorkSet, error) {
	return s.ws, nil
}

// difficultyFor returns a defensive copy of the static
// difficulty. Defensive copy keeps the verifier's internal
// arithmetic from accidentally mutating the service's stored
// value (mining.Verifier doesn't, today, but the contract
// allows it).
func (s *Service) difficultyFor(_ uint64) (*big.Int, error) {
	return new(big.Int).Set(s.difficulty), nil
}

// chainAdapter bridges *chain.BlockProducer to
// mining.ChainView. Producer.TipHeight is the obvious half;
// HeaderHashAt is a hex-decode of GetBlock(h).Hash. A height
// that has no block (e.g. above tip) returns ok=false, which
// the verifier maps to ReasonHeaderMismatch — the right
// rejection reason because a proof claiming a height the
// chain doesn't recognise is exactly a header-mismatch
// rejection.
type chainAdapter struct {
	producer *chain.BlockProducer
}

func (c chainAdapter) TipHeight() uint64 { return c.producer.TipHeight() }
func (c chainAdapter) HeaderHashAt(h uint64) ([32]byte, bool) {
	blk, ok := c.producer.GetBlock(h)
	if !ok {
		return [32]byte{}, false
	}
	var hdr [32]byte
	if err := decodeHexInto(hdr[:], blk.Hash); err != nil {
		return [32]byte{}, false
	}
	return hdr, true
}

// decodeHexInto fills dst from the hex-encoded source string.
// Returns an error on malformed hex or wrong length.
func decodeHexInto(dst []byte, src string) error {
	b, err := hex.DecodeString(src)
	if err != nil {
		return fmt.Errorf("hex decode: %w", err)
	}
	if len(b) != len(dst) {
		return fmt.Errorf("hex length %d != %d", len(b), len(dst))
	}
	copy(dst, b)
	return nil
}

// cloneWorkSet returns a deep copy so canonicalisation /
// validation in New cannot reach back into the caller's
// slice. The cell ID slice is the only subfield that aliases
// caller memory by default; we copy it explicitly.
func cloneWorkSet(in mining.WorkSet) mining.WorkSet {
	out := mining.WorkSet{Batches: make([]mining.Batch, len(in.Batches))}
	for i, b := range in.Batches {
		cells := make([]mining.ParentCellRef, len(b.Cells))
		for j, c := range b.Cells {
			id := append([]byte(nil), c.ID...)
			cells[j] = mining.ParentCellRef{ID: id, ContentHash: c.ContentHash}
		}
		out.Batches[i] = mining.Batch{Cells: cells}
	}
	return out
}

// nonEmptyAddressValidator is the default Addresses
// implementation: accept any non-empty string. Adequate for
// bring-up testnet; production should swap in the real
// wallet-address parser via cfg.Addresses.
type nonEmptyAddressValidator struct{}

func (nonEmptyAddressValidator) ValidateAddress(a string) error {
	if a == "" {
		return errors.New("miner_addr is empty")
	}
	return nil
}

// acceptAllBatchValidator is the default Batches
// implementation: accept every structural batch. Bring-up
// posture; the static WorkSet is the authoritative shape so
// further structural validation is redundant.
type acceptAllBatchValidator struct{}

func (acceptAllBatchValidator) ValidateBatch(_ mining.Batch) error { return nil }

// Compile-time guard. Any drift in api.MiningService's method
// set will surface here at build time, not at boot when the
// type assertion in api.SetMiningService fires.
var _ api.MiningService = (*Service)(nil)
