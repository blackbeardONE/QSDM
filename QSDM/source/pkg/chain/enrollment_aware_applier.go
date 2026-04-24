package chain

// enrollment_aware_applier.go: a StateApplier shim that routes
// enrollment-tagged transactions (ContractID == enrollment.ContractID)
// through an EnrollmentApplier and falls back to the underlying
// *AccountStore for ordinary transfers.
//
// Scope of this commit (Phase 2c-vii, block-apply wiring for the
// simple single-validator path):
//
//   - EnrollmentAwareApplier implements pkg/chain.StateApplier
//     so it can be passed directly to NewBlockProducer.
//   - Height threading is done via a caller-supplied HeightFn so
//     the shim has no back-reference to *BlockProducer and stays
//     independently testable. The canonical wiring is
//     `HeightFn: func() uint64 { return bp.TipHeight() + 1 }`
//     set AFTER the producer is constructed (BlockProducer has no
//     circular dep on this type).
//
//     IMPORTANT: use BlockProducer.TipHeight, NOT ChainHeight.
//     HeightFn is invoked from inside bp.applier.ApplyTx, which
//     runs while ProduceBlock already holds bp.mu; calling
//     ChainHeight from that context deadlocks (non-reentrant
//     mutex). TipHeight is lock-free and specifically designed
//     for this call site.
//   - Sweep is a separate public call intended to run once per
//     sealed block, after the block's transactions are applied.
//     Today's BlockProducer does not have a typed post-seal hook
//     that carries the block height, so operators invoke
//     Sweep(h) themselves from their own finalisation logic (see
//     the integration test for the canonical pattern).
//
// Explicitly out of scope for this commit:
//
//   - ChainReplayApplier semantics. Cloning the EnrollmentState
//     for speculative replay (pre-seal BFT or TryAppendExternalBlock)
//     is a future commit. The shim deliberately does NOT implement
//     ChainReplayApplier so the producer's type-assert fails fast
//     with ErrExternalAppendNeedsAccountStore instead of silently
//     producing a divergent state root.
//
//   - Mempool admission gate. PoolValidator integration is an
//     orthogonal change (stateless ValidateEnrollFields /
//     ValidateUnenrollFields) that can land independently.

import (
	"errors"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// EnrollmentAwareApplier is a StateApplier that dispatches on
// tx.ContractID. Construct via NewEnrollmentAwareApplier.
//
// The shim is concurrency-safe because both AccountStore and
// InMemoryState (the typical EnrollmentStateMutator) hold their
// own locks; the only mutable field owned by the shim is the
// optional height provider, which is guarded by mu.
type EnrollmentAwareApplier struct {
	accounts   *AccountStore
	enrollment *EnrollmentApplier

	mu       sync.RWMutex
	heightFn func() uint64
}

// NewEnrollmentAwareApplier wires the router. `accounts` is
// required. `ea` may be nil, in which case the shim behaves
// exactly like the bare AccountStore (enrollment txs are
// rejected with ErrNotEnrollmentTx-style errors; this is the
// recommended form for nodes that have NOT activated the v2
// enrollment feature yet).
//
// Panics on nil `accounts` because a missing account store is a
// programming error at boot, not a per-tx condition.
func NewEnrollmentAwareApplier(accounts *AccountStore, ea *EnrollmentApplier) *EnrollmentAwareApplier {
	if accounts == nil {
		panic("chain: NewEnrollmentAwareApplier requires non-nil *AccountStore")
	}
	return &EnrollmentAwareApplier{
		accounts:   accounts,
		enrollment: ea,
	}
}

// SetHeightFn installs (or clears) the block-height provider.
// `fn == nil` disables the provider and causes enrollment
// txs to be rejected with a clear error rather than applied at
// an undefined height. The canonical wiring is:
//
//	bp := chain.NewBlockProducer(pool, aware, cfg)
//	aware.SetHeightFn(func() uint64 { return bp.TipHeight() + 1 })
//
// Post-construction installation is intentional: BlockProducer
// is built AFTER the applier, so the closure over bp must be
// deferred.
//
// MUST be lock-free. HeightFn is invoked from inside ApplyTx
// while ProduceBlock holds bp.mu; any function that re-enters
// bp.mu (e.g. bp.ChainHeight, bp.LatestBlock) will deadlock.
// Use bp.TipHeight, which is backed by atomic.Uint64.
func (a *EnrollmentAwareApplier) SetHeightFn(fn func() uint64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.heightFn = fn
}

// ApplyTx implements StateApplier. Routes on tx.ContractID:
//
//   - enrollment.ContractID → EnrollmentApplier.ApplyEnrollmentTx
//     using the current height from HeightFn.
//   - anything else → AccountStore.ApplyTx (the plain transfer path).
//
// When the enrollment applier is nil but an enrollment tx is
// received, the tx is REJECTED with ErrEnrollmentNotWired so it
// never silently apples to the account store (which would
// ignore the Payload and apply it as a zero-amount transfer,
// corrupting nonce ordering on replay).
func (a *EnrollmentAwareApplier) ApplyTx(tx *mempool.Tx) error {
	if a == nil {
		return errors.New("chain: nil EnrollmentAwareApplier")
	}
	if tx == nil {
		return errors.New("chain: nil tx")
	}
	if tx.ContractID == enrollment.ContractID {
		if a.enrollment == nil {
			return ErrEnrollmentNotWired
		}
		h, ok := a.currentHeight()
		if !ok {
			return ErrEnrollmentHeightUnset
		}
		return a.enrollment.ApplyEnrollmentTx(tx, h)
	}
	return a.accounts.ApplyTx(tx)
}

// StateRoot implements StateApplier. Today the state root is
// sourced purely from the account store; enrollment state
// mutations that flow through AccountStore (stake debit / sweep
// credit) are reflected here. Folding enrollment-state hashes
// into the block state root is a follow-on consensus change.
func (a *EnrollmentAwareApplier) StateRoot() string {
	if a == nil || a.accounts == nil {
		return ""
	}
	return a.accounts.StateRoot()
}

// Sweep releases every enrollment whose unbond window matures
// at `height`, crediting each record's stake back to its owner.
// Intended to be called exactly once per sealed block, AFTER
// the block's transactions have been applied.
//
// Returns the list of releases (for receipts / monitoring) and
// any error from the underlying state. A nil enrollment applier
// returns (nil, nil) so callers can Sweep unconditionally from
// their block-finalisation path without branching.
func (a *EnrollmentAwareApplier) Sweep(height uint64) ([]enrollment.UnbondRelease, error) {
	if a == nil || a.enrollment == nil {
		return nil, nil
	}
	return a.enrollment.SweepMaturedEnrollments(height)
}

// Accounts exposes the underlying account store for callers
// that need to observe balance state directly (e.g. tests,
// genesis wiring, wallet RPC). NOT for general mutation — use
// the ApplyTx path so enrollment routing stays consistent.
func (a *EnrollmentAwareApplier) Accounts() *AccountStore {
	if a == nil {
		return nil
	}
	return a.accounts
}

// EnrollmentApplier returns the configured enrollment applier,
// or nil if enrollment is not wired on this node.
func (a *EnrollmentAwareApplier) EnrollmentApplier() *EnrollmentApplier {
	if a == nil {
		return nil
	}
	return a.enrollment
}

// currentHeight reads the configured height provider. Returns
// (0, false) if no provider is set, which ApplyTx surfaces as
// ErrEnrollmentHeightUnset so misconfiguration is loud rather
// than silently stamping every enroll with height 0.
func (a *EnrollmentAwareApplier) currentHeight() (uint64, bool) {
	a.mu.RLock()
	fn := a.heightFn
	a.mu.RUnlock()
	if fn == nil {
		return 0, false
	}
	return fn(), true
}

// Sentinel errors returned by ApplyTx when enrollment routing
// is impossible. Both are surfaced as tx-level rejections (not
// panics) because they describe misconfiguration that should be
// visible in block receipts and fixable without restarting.
var (
	// ErrEnrollmentNotWired is returned when a tx tagged as an
	// enrollment transaction arrives at a node that has no
	// EnrollmentApplier configured. Typical cause: a v2-aware
	// miner submitted against a v1-only validator.
	ErrEnrollmentNotWired = errors.New("chain: enrollment tx received but no EnrollmentApplier is wired")

	// ErrEnrollmentHeightUnset is returned when an enrollment
	// tx is received but no HeightFn has been installed on the
	// EnrollmentAwareApplier. This is strictly a wiring bug
	// (the post-construction SetHeightFn call was missed) and
	// always fatal for the offending tx.
	ErrEnrollmentHeightUnset = errors.New("chain: EnrollmentAwareApplier has no HeightFn set")
)

// Compile-time interface assertions.
var (
	_ StateApplier = (*EnrollmentAwareApplier)(nil)
)

// NOTE (deliberate non-implementation): we do NOT satisfy
// ChainReplayApplier. See the file-header comment for rationale.
// The producer's type assertions in TryAppendExternalBlock and
// SetPreSealBFTRound will therefore refuse this applier, which
// is the intended safety behaviour for this phase.
