package v2wiring

// Package v2wiring centralises the boot-time assembly of the v2
// mining surface (on-chain enrollment + slashing + observability)
// so cmd/qsdm/main.go does not have to repeat ~50 lines of
// collaborator construction. The package exists for two reasons
// the inline form does not satisfy:
//
//  1. Dependency-inversion. pkg/chain MUST NOT import
//     pkg/monitoring (the import cycle was closed via
//     chain.MetricsRecorder + chain.SetChainMetricsRecorder
//     in pkg/chain/events.go and pkg/monitoring/chain_recorder.go).
//     Wiring code that crosses BOTH packages cannot live in
//     either of them. Putting it here keeps the boundary clean.
//
//  2. Testability. The same Wire(...) call shape used by
//     production cmd/qsdm/main.go is also what the integration
//     test in v2wiring_test.go exercises. A drift between
//     production and test would be caught by the test failing,
//     not by a silent regression in mainnet.
//
// Scope:
//
//   - Constructs *enrollment.InMemoryState, *EnrollmentApplier,
//     *EnrollmentAwareApplier, optional *SlashApplier.
//   - Registers the monitoring state-provider so the four
//     `qsdm_enrollment_*` gauges populate.
//   - Composes a stacked mempool admission gate
//     (slashing > enrollment > base predicate) so each ContractID
//     family hits its own stateless validators before the
//     operator's POL/BFT gate.
//   - Wires the producer via SetHeightFn and assigns the
//     SealedBlockHook for matured-stake auto-sweep.
//   - Exposes the live mempool to the api/v1/mining/{enroll,
//     unenroll, slash} HTTP handlers via the matching
//     api.Set*Mempool() helpers, and the live registry to
//     api/v1/mining/enrollment/{node_id} via
//     api.SetEnrollmentRegistry. One Wire() call lights up
//     the entire v2 mining HTTP surface.
//
// Out of scope:
//
//   - mining.VerifierConfig.Attestation wiring (that lives in
//     the mining proof submission path and is wired separately
//     in cmd/qsdmminer-* binaries).
//   - Block production lifecycle, gossip, BFT.

import (
	"errors"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/doublemining"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// Config is the input bundle Wire consumes. Every field is
// REQUIRED unless explicitly marked optional. The zero value is
// invalid and Wire returns an error on it rather than papering
// over a missing collaborator.
type Config struct {
	// Accounts is the live AccountStore the producer mutates.
	// REQUIRED. The same instance must be passed to all v2
	// appliers so balance debits land in one ledger.
	Accounts *chain.AccountStore

	// Pool is the mempool whose admission gate we compose
	// against. REQUIRED. Wire calls SetAdmissionChecker on it
	// exactly once.
	Pool *mempool.Mempool

	// BaseAdmit is the operator's pre-existing admission gate
	// (e.g. POL extension predicate, BFT-committed predicate).
	// May be nil; in that case the gate accepts every tx that
	// the enrollment validators allow.
	BaseAdmit func(*mempool.Tx) error

	// SlashRewardBPS is the basis-points reward the slasher
	// receives from each successful drain. The protocol cap is
	// chain.SlashRewardCap (50%); higher values cause
	// NewSlashApplier to panic. Use 0 for "burn everything"
	// or chain.SlashRewardCap for "max reward".
	SlashRewardBPS uint16

	// LogSweepError is invoked when the post-seal hook's call
	// to SweepMaturedEnrollments returns an error. Nil = drop
	// silently (matches the legacy OnSealed contract). Used
	// for operational visibility, not for retry — the next
	// sealed block re-runs the sweep.
	LogSweepError func(height uint64, err error)
}

// Wired is the output bundle. cmd/qsdm/main.go consumes:
//
//   - .StateApplier as the chain.StateApplier passed to
//     NewBlockProducer (drop-in for the bare AccountStore).
//   - .Aware to call SetHeightFn(...) AFTER the producer is
//     constructed (canonical Phase 2c-vii pattern).
//   - .SealedBlockHook to assign to producer.OnSealedBlock.
//
// .EnrollmentState and .Slasher are exposed for tests and for
// future call sites that need direct registry access (e.g. a
// /api/v1/mining/enrollment/{node_id} GET).
type Wired struct {
	StateApplier    chain.StateApplier
	Aware           *chain.EnrollmentAwareApplier
	EnrollmentState *enrollment.InMemoryState
	Enrollment      *chain.EnrollmentApplier
	Slasher         *chain.SlashApplier
	SealedBlockHook func(*chain.Block)
}

// Wire assembles the v2 mining surface against the supplied
// collaborators. Returns an error rather than panicking on
// invalid input so cmd/qsdm/main.go can degrade to v1-only mode
// (i.e. continue booting without v2 enrollment) if a collaborator
// is missing — though Validate() rejects that case for safety.
//
// SlashApplier construction is best-effort: if the production
// dispatcher cannot be built (e.g. the doublemining factory
// returns an error), Wired.Slasher is left nil and the operator
// gets a clear error from this function. The aware applier is
// still returned with slashing OFF, so v2 enrollment can run
// even if slashing wiring is broken — slash txs just bounce
// with chain.ErrSlashingNotWired until fixed.
func Wire(cfg Config) (*Wired, error) {
	if cfg.Accounts == nil {
		return nil, errors.New("v2wiring: Config.Accounts is required")
	}
	if cfg.Pool == nil {
		return nil, errors.New("v2wiring: Config.Pool is required")
	}
	if cfg.SlashRewardBPS > chain.SlashRewardCap {
		return nil, fmt.Errorf(
			"v2wiring: SlashRewardBPS=%d exceeds chain.SlashRewardCap=%d",
			cfg.SlashRewardBPS, chain.SlashRewardCap)
	}

	state := enrollment.NewInMemoryState()
	enrollAp := chain.NewEnrollmentApplier(cfg.Accounts, state)
	aware := chain.NewEnrollmentAwareApplier(cfg.Accounts, enrollAp)

	// Slasher arm. Build the production dispatcher with the
	// real registry; on error, return a clear wiring failure
	// — slashing wiring drift is exactly the kind of silent
	// regression this package exists to prevent.
	disp, err := doublemining.NewProductionSlashingDispatcher(
		enrollment.NewStateBackedRegistry(state),
		nil, // empty deny-list at boot; governance can append later.
		0,   // forgedattest cap = forgedattest.DefaultMaxSlashDust
		0,   // doublemining cap = doublemining.DefaultMaxSlashDust
	)
	if err != nil {
		return nil, fmt.Errorf("v2wiring: slash dispatcher: %w", err)
	}
	slasher := chain.NewSlashApplier(
		cfg.Accounts, state, disp, cfg.SlashRewardBPS,
	)
	aware.SetSlashApplier(slasher)

	// Monitoring gauge provider. Replaces any prior provider
	// installed by an earlier boot — the underlying atomic.Value
	// is overwrite-on-set, so multiple Wire() calls in the same
	// process (e.g. an embedded validator restart) leave the
	// gauges consistent with the most recent state.
	monitoring.SetEnrollmentStateProvider(
		monitoring.NewEnrollmentInMemoryStateProvider(state),
	)

	// Mempool admission. Two stateless layers stacked on top of
	// the operator-supplied base predicate:
	//
	//   - slashing.AdmissionChecker  (slash txs)
	//   - enrollment.AdmissionChecker (enroll/unenroll txs)
	//   - cfg.BaseAdmit               (everything else: POL/BFT)
	//
	// Each layer only intercepts its own ContractID and
	// delegates other contracts down the chain, so layer order
	// is structurally safe but kept stable for readability:
	// slash > enroll > base mirrors the conceptual blast radius
	// (a slash tx is the most consequential so its validators
	// run first).
	cfg.Pool.SetAdmissionChecker(
		slashing.AdmissionChecker(
			enrollment.AdmissionChecker(cfg.BaseAdmit)))

	// HTTP handler hookup. All four mining endpoints
	//
	//   POST /api/v1/mining/enroll
	//   POST /api/v1/mining/unenroll
	//   POST /api/v1/mining/slash
	//   GET  /api/v1/mining/enrollment/{node_id}
	//
	// are no-ops without their respective Set*() install —
	// each returns 503 Service Unavailable until set. Wired
	// together so a validator that brings up v2 enrollment
	// brings up the full read+write surface in one call.
	//
	// SetEnrollmentRegistry exposes the same *InMemoryState
	// the appliers mutate — one source of truth for chain
	// state, no separate read replica or cache.
	api.SetEnrollmentMempool(cfg.Pool)
	api.SetSlashMempool(cfg.Pool)
	api.SetEnrollmentRegistry(state)

	hook := aware.SealedBlockHook(cfg.LogSweepError)

	return &Wired{
		StateApplier:    aware,
		Aware:           aware,
		EnrollmentState: state,
		Enrollment:      enrollAp,
		Slasher:         slasher,
		SealedBlockHook: hook,
	}, nil
}

// ReinstallAdmissionGate replaces the pool's admission checker
// with enrollment.AdmissionChecker(prev), preserving the same
// shape Wire installed but with a new BaseAdmit predicate. Use
// when the operator's BaseAdmit closes over collaborators that
// only exist after Wire is called (typical example:
// cmd/qsdm/main.go's POL/BFT predicate, which closes over
// polFollower + liveBFT, both built later in the same boot
// path).
func ReinstallAdmissionGate(pool *mempool.Mempool, prev func(*mempool.Tx) error) {
	if pool == nil {
		return
	}
	// Mirror Wire()'s stack: slashing > enrollment > prev.
	pool.SetAdmissionChecker(
		slashing.AdmissionChecker(
			enrollment.AdmissionChecker(prev)))
}

// AttachToProducer wires the post-construction half of the
// EnrollmentAwareApplier contract into a freshly-built
// BlockProducer:
//
//   - SetHeightFn(bp.TipHeight + 1)
//   - bp.OnSealedBlock = w.SealedBlockHook
//
// Split out from Wire because BlockProducer is constructed AFTER
// the StateApplier (Wire's output) is available — the producer
// closes over the applier, the applier closes back over the
// producer's TipHeight. AttachToProducer is the second half of
// that knot.
//
// Idempotent: calling twice on the same producer is a no-op
// because SetHeightFn replaces the prior fn and OnSealedBlock
// replaces the prior assignment. Useful for tests that rebuild
// the producer.
func (w *Wired) AttachToProducer(bp *chain.BlockProducer) {
	if w == nil || bp == nil {
		return
	}
	w.Aware.SetHeightFn(func() uint64 { return bp.TipHeight() + 1 })
	bp.OnSealedBlock = w.SealedBlockHook
}
