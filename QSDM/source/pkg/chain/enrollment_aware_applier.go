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
//     The canonical wiring is via BlockProducer.OnSealedBlock,
//     either with the SealedBlockHook helper on this type:
//
//	    bp.OnSealedBlock = aware.SealedBlockHook(nil)
//
//     or by passing a custom error handler:
//
//	    bp.OnSealedBlock = aware.SealedBlockHook(func(h uint64, err error) {
//	        log.Printf("sweep at height %d failed: %v", h, err)
//	    })
//
//     Operators that need fully custom sweep policy can still
//     call Sweep(h) directly from their own finalisation logic.
//
// Explicitly out of scope for this file:
//
//   - Mempool admission gate. Stateless validation
//     (ValidateEnrollFields / ValidateUnenrollFields) lives in
//     pkg/mining/enrollment/admit.go and is wired via
//     mempool.SetAdmissionChecker, independently of this shim.
//
// Updates from prior phases:
//
//   - ChainReplayApplier IS now satisfied. ChainReplayClone +
//     RestoreFromChainReplay deep-copy both the AccountStore
//     and the EnrollmentState (the latter via the optional
//     enrollment.CloneableState contract; *InMemoryState
//     satisfies it). Pre-seal BFT and TryAppendExternalBlock
//     therefore work end-to-end with enrollment txs.

import (
	"errors"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
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
	slasher    *SlashApplier

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
	if tx.ContractID == slashing.ContractID {
		if a.slasher == nil {
			return ErrSlashingNotWired
		}
		h, ok := a.currentHeight()
		if !ok {
			return ErrEnrollmentHeightUnset
		}
		return a.slasher.ApplySlashTx(tx, h)
	}
	return a.accounts.ApplyTx(tx)
}

// SetSlashApplier installs (or clears) the slashing applier.
// Opt-in — callers that don't set it reject all slash txs with
// ErrSlashingNotWired, matching the enrollment opt-in pattern.
//
// The slash applier MUST share the same *AccountStore and
// EnrollmentState (via its SlasherStateMutator contract) as
// this shim's enrollment applier, otherwise routing would read
// from one state tree and mutate another. The constructor does
// not (cannot) enforce this — it's a wiring invariant the caller
// owns. Production wiring uses a single *enrollment.InMemoryState
// passed to both NewEnrollmentApplier and NewSlashApplier.
func (a *EnrollmentAwareApplier) SetSlashApplier(sa *SlashApplier) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.slasher = sa
}

// SlashApplier returns the configured slashing applier, or nil
// if slashing is not wired on this node.
func (a *EnrollmentAwareApplier) SlashApplier() *SlashApplier {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.slasher
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

// SealedBlockHook returns a function suitable for assignment to
// BlockProducer.OnSealedBlock that automatically invokes
// Sweep(blk.Height) after every sealed block. Pass `onErr` to
// observe sweep failures (which are otherwise swallowed because
// the post-seal hook contract has no error path); nil drops
// errors silently, matching the legacy OnSealed behaviour.
//
// The returned hook is concurrency-safe (BlockProducer fires it
// outside bp.mu, and Sweep takes its own locks via the
// EnrollmentApplier / EnrollmentState).
//
// If the shim has no enrollment applier wired, the hook is a
// no-op and never calls onErr — installing it on a v1-only node
// is therefore safe and idempotent.
func (a *EnrollmentAwareApplier) SealedBlockHook(onErr func(height uint64, err error)) func(*Block) {
	return func(blk *Block) {
		if a == nil || a.enrollment == nil || blk == nil {
			return
		}
		if _, err := a.Sweep(blk.Height); err != nil && onErr != nil {
			onErr(blk.Height, err)
		}
	}
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

	// ErrSlashingNotWired is returned when a slash tx arrives
	// at a node that has no SlashApplier configured. Symmetric
	// to ErrEnrollmentNotWired; typical cause is a v2-aware
	// peer submitting to a validator that hasn't enabled
	// slashing yet.
	ErrSlashingNotWired = errors.New("chain: slash tx received but no SlashApplier is wired")
)

// Compile-time interface assertions.
var (
	_ StateApplier       = (*EnrollmentAwareApplier)(nil)
	_ ChainReplayApplier = (*EnrollmentAwareApplier)(nil)
)

// ChainReplayClone implements ChainReplayApplier. Returns a new
// EnrollmentAwareApplier whose AccountStore and EnrollmentState
// are deep copies of the receiver's. Mutations on the clone do
// NOT affect the live applier; abandoning the clone (no Restore)
// is the speculative-rollback path.
//
// Panics if the wired EnrollmentStateMutator does not satisfy
// enrollment.CloneableState — that's a wiring bug that must
// surface at boot or BFT-replay setup, not silently degrade
// finality. Production wiring uses *enrollment.InMemoryState
// (or any future state implementation that adds Clone/Restore).
func (a *EnrollmentAwareApplier) ChainReplayClone() ChainReplayApplier {
	if a == nil {
		return nil
	}
	clone := &EnrollmentAwareApplier{
		accounts: a.accounts.Clone(),
	}
	// Clone the enrollment state exactly once and share it
	// between the enrollment applier and the slasher so both
	// routes see a single consistent snapshot. If they each
	// got a fresh clone, enroll/unenroll and slash would
	// diverge on the same speculative block.
	var sharedMutator EnrollmentStateMutator
	if a.enrollment != nil {
		ces, ok := a.enrollment.State.(enrollment.CloneableState)
		if !ok {
			panic("chain: EnrollmentAwareApplier.ChainReplayClone: " +
				"wired EnrollmentStateMutator does not implement " +
				"enrollment.CloneableState — speculative replay is unsafe")
		}
		stateClone := ces.Clone()
		clonedMutator, ok := stateClone.(EnrollmentStateMutator)
		if !ok {
			panic("chain: EnrollmentAwareApplier.ChainReplayClone: " +
				"cloned state does not satisfy EnrollmentStateMutator")
		}
		sharedMutator = clonedMutator
		clone.enrollment = NewEnrollmentApplier(clone.accounts, sharedMutator)
	}
	// Mirror the slasher against the cloned account store and
	// the same state snapshot. Dispatcher + RewardBPS are
	// deterministic / stateless so they pass through by value.
	a.mu.RLock()
	liveSlasher := a.slasher
	clone.heightFn = a.heightFn
	a.mu.RUnlock()
	if liveSlasher != nil {
		sm, ok := sharedMutator.(SlasherStateMutator)
		if !ok {
			// If enrollment was NOT wired but slashing was,
			// we must clone the slasher's own state. This
			// path is atypical (slashing without enrollment
			// is nonsensical) but we defend against it.
			if sharedMutator == nil {
				if ces, ok := liveSlasher.State.(enrollment.CloneableState); ok {
					clonedState := ces.Clone()
					if m, ok := clonedState.(SlasherStateMutator); ok {
						sm = m
					}
				}
			}
			if sm == nil {
				panic("chain: EnrollmentAwareApplier.ChainReplayClone: " +
					"wired SlasherStateMutator cannot be cloned")
			}
		}
		clone.slasher = NewSlashApplier(
			clone.accounts,
			sm,
			liveSlasher.Dispatcher,
			liveSlasher.RewardBPS,
		)
	}
	return clone
}

// RestoreFromChainReplay implements ChainReplayApplier. Replaces
// the receiver's contents with those of `from`, which MUST be a
// snapshot returned by ChainReplayClone on the same applier
// (or one in the same family — same concrete EnrollmentState
// type). Errors on type mismatch.
//
// Used as the abort path for TryAppendExternalBlock when live
// apply diverges from the replay state root, and for any
// operator-driven rollback. Atomic: AccountStore restore is
// done first; if it fails, the EnrollmentState is not touched.
func (a *EnrollmentAwareApplier) RestoreFromChainReplay(from ChainReplayApplier) error {
	if a == nil {
		return errors.New("chain: nil EnrollmentAwareApplier on RestoreFromChainReplay")
	}
	other, ok := from.(*EnrollmentAwareApplier)
	if !ok || other == nil {
		return errors.New("chain: RestoreFromChainReplay expects *EnrollmentAwareApplier snapshot")
	}
	if err := a.accounts.RestoreFromChainReplay(other.accounts); err != nil {
		return err
	}
	if a.enrollment == nil && other.enrollment == nil {
		return nil
	}
	if a.enrollment == nil || other.enrollment == nil {
		return errors.New("chain: RestoreFromChainReplay enrollment applier presence mismatch")
	}
	srcState, ok := other.enrollment.State.(enrollment.CloneableState)
	if !ok {
		return errors.New("chain: source enrollment state does not implement enrollment.CloneableState")
	}
	dstState, ok := a.enrollment.State.(enrollment.CloneableState)
	if !ok {
		return errors.New("chain: live enrollment state does not implement enrollment.CloneableState")
	}
	return dstState.Restore(srcState)
}
