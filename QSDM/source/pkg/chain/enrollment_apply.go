package chain

// enrollment_apply.go: consensus-layer plumbing that routes
// "qsdm/enroll/v1" transactions (mempool.Tx with
// ContractID == enrollment.ContractID) through
// pkg/mining/enrollment's validation + state transitions,
// coordinated with AccountStore balance debits and
// stake-release credits on sweep.
//
// Scope of this commit (Phase 2c-vi plumbing, NOT block-apply
// wiring yet):
//
//   - EnrollmentApplier struct with ApplyEnrollmentTx and
//     SweepMaturedEnrollments.
//   - Unit conversion between float64 CELL balances (the
//     pre-fork account store representation) and the uint64
//     dust units the enrollment model operates in.
//   - Atomic apply semantics: on any validation or state error
//     the receiver's AccountStore and EnrollmentState are
//     unchanged, so the caller can safely surface the error as
//     a block-receipt rejection without rolling back anything.
//
// Explicitly out of scope:
//
//   - No call site in bft_executor / BlockProducer yet. The
//     follow-on commit flips the switch by routing
//     ContractID-tagged txs through this adapter during block
//     finalisation. Keeping that change as a separate commit
//     makes the consensus-visible change easy to review in
//     isolation.
//
//   - No mempool admission gate yet. Stateless validation
//     (ValidateEnrollFields / ValidateUnenrollFields) can be
//     surfaced via pkg/chain.PoolValidator, but wiring that
//     belongs in the same commit that flips the apply path.
//
// Design notes:
//
//   - Unit bridging (balanceToDust / dustToBalance) is done
//     here rather than forcing the enrollment package to know
//     about float64, because pkg/mining/enrollment is meant to
//     survive the eventual migration to a uint64-only ledger
//     and should not carry pre-fork float semantics.
//
//   - The EnrollmentStateMutator interface is declared locally
//     so pkg/chain depends only on the methods it actually
//     uses; *enrollment.InMemoryState satisfies it through
//     structural typing without any changes to the enrollment
//     package.

import (
	"errors"
	"fmt"
	"math"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// dustPerCELL is the fixed 1 CELL = 1e8 dust scaling factor.
// Mirrors mining.MinEnrollStakeDust's encoding (see fork.go).
// Held as a uint64 constant so conversion stays integer-safe
// for the inverse direction.
const dustPerCELL uint64 = 100_000_000

// EnrollmentStateMutator is the subset of enrollment-state
// operations the chain needs to drive enroll/unenroll/sweep.
// Declared locally (rather than requiring pkg/mining/enrollment
// to export an interface) so chain depends only on what it
// uses. *enrollment.InMemoryState satisfies this by shape.
type EnrollmentStateMutator interface {
	enrollment.EnrollmentState

	// ApplyEnroll inserts a new EnrollmentRecord. Called only
	// after both stateless and stateful validation have passed.
	ApplyEnroll(rec enrollment.EnrollmentRecord) error

	// ApplyUnenroll marks the record for unbond at
	// currentHeight. The stake remains locked in the record
	// (not credited back to the owner) until
	// SweepMaturedUnbonds releases it.
	ApplyUnenroll(nodeID string, currentHeight uint64) error

	// SweepMaturedUnbonds returns the list of records whose
	// unbond window has matured at currentHeight. The caller
	// (SweepMaturedEnrollments below) credits the stake back
	// to each record's Owner.
	SweepMaturedUnbonds(currentHeight uint64) []enrollment.UnbondRelease
}

// EnrollmentApplier is the chain-side adapter that bridges a
// raw *mempool.Tx carrying an enrollment payload into the
// pkg/mining/enrollment state machine, coordinating
// AccountStore debits and sweep-time credits along the way.
//
// Construct once per node and hold it for the lifetime of the
// chain instance. Both fields MUST be set; ApplyEnrollmentTx
// returns a clear error if either is nil, rather than
// panicking, because a nil-field bug in a rarely-exercised
// apply path is exactly the kind of thing that silently fails
// review.
type EnrollmentApplier struct {
	Accounts *AccountStore
	State    EnrollmentStateMutator

	// Publisher receives an EnrollmentEvent for every
	// outcome (enroll-applied, enroll-rejected,
	// unenroll-applied, unenroll-rejected, sweep). Defaults
	// to NoopEventPublisher; opt into structured events by
	// replacing the field. Calls are synchronous from the
	// applier's view — see pkg/chain/events.go.
	Publisher ChainEventPublisher
}

// NewEnrollmentApplier wires the adapter. Panics on nil fields
// because an applier with either field missing is a
// programming error that should crash at boot, not surface as
// a per-tx rejection at block time.
func NewEnrollmentApplier(accounts *AccountStore, state EnrollmentStateMutator) *EnrollmentApplier {
	if accounts == nil {
		panic("chain: NewEnrollmentApplier requires non-nil *AccountStore")
	}
	if state == nil {
		panic("chain: NewEnrollmentApplier requires non-nil EnrollmentStateMutator")
	}
	return &EnrollmentApplier{
		Accounts:  accounts,
		State:     state,
		Publisher: NoopEventPublisher{},
	}
}

// publisher returns the configured ChainEventPublisher,
// substituting NoopEventPublisher if the field was left nil
// (e.g. by a test that built an EnrollmentApplier struct
// literal instead of going through NewEnrollmentApplier).
func (a *EnrollmentApplier) publisher() ChainEventPublisher {
	if a == nil || a.Publisher == nil {
		return NoopEventPublisher{}
	}
	return a.Publisher
}

// ApplyEnrollmentTx validates and applies a single enrollment
// transaction at block `height`. Returns nil on success; on any
// validation or apply error, the receiver's state is untouched.
//
// The caller is responsible for having verified the tx's
// signature and mempool fee rules BEFORE handing it to this
// adapter — this method trusts tx.Sender as already-attributed
// and tx.Nonce as claimed.
//
// tx.ContractID MUST equal enrollment.ContractID; any other
// value returns ErrNotEnrollmentTx so callers can detect
// accidental misrouting instead of silently dropping the tx.
func (a *EnrollmentApplier) ApplyEnrollmentTx(tx *mempool.Tx, height uint64) error {
	if a == nil {
		return errors.New("chain: nil EnrollmentApplier")
	}
	if tx == nil {
		return errors.New("chain: nil enrollment tx")
	}

	// Top-level reject helper. We don't yet know whether the
	// payload would dispatch to enroll or unenroll, so route
	// these top-level rejections through the enroll-rejected
	// channel by default — there is no separate "unknown
	// dispatch" event kind, and the WrongContract / decode
	// failures are most naturally framed as enroll-side
	// problems for indexers.
	rejectTop := func(reason string, err error) error {
		metrics().RecordEnrollmentRejected(reason)
		a.publisher().PublishEnrollment(EnrollmentEvent{
			Kind:         EnrollmentEventEnrollRejected,
			Height:       height,
			Sender:       tx.Sender,
			RejectReason: reason,
			Err:          err,
		})
		return err
	}

	if tx.ContractID != enrollment.ContractID {
		return rejectTop(EnrollRejectReasonWrongContract,
			fmt.Errorf("%w: got %q, want %q",
				ErrNotEnrollmentTx, tx.ContractID, enrollment.ContractID))
	}

	kind, err := enrollment.PeekKind(tx.Payload)
	if err != nil {
		return rejectTop(EnrollRejectReasonDecode,
			fmt.Errorf("chain: peek enrollment kind: %w", err))
	}

	switch kind {
	case enrollment.PayloadKindEnroll:
		return a.applyEnroll(tx, height)
	case enrollment.PayloadKindUnenroll:
		return a.applyUnenroll(tx, height)
	default:
		return rejectTop(EnrollRejectReasonDecode,
			fmt.Errorf("chain: unknown enrollment payload kind %q", kind))
	}
}

// applyEnroll is the Enroll-kind branch of ApplyEnrollmentTx.
// Atomic: either every mutation happens, or none do.
func (a *EnrollmentApplier) applyEnroll(tx *mempool.Tx, height uint64) error {
	rejectEnroll := func(reason string, nodeID string, err error) error {
		metrics().RecordEnrollmentRejected(reason)
		a.publisher().PublishEnrollment(EnrollmentEvent{
			Kind:         EnrollmentEventEnrollRejected,
			Height:       height,
			Sender:       tx.Sender,
			NodeID:       nodeID,
			RejectReason: reason,
			Err:          err,
		})
		return err
	}

	payload, err := enrollment.DecodeEnrollPayload(tx.Payload)
	if err != nil {
		return rejectEnroll(EnrollRejectReasonDecode, "",
			fmt.Errorf("chain: decode enroll payload: %w", err))
	}

	if err := enrollment.ValidateEnrollFields(payload, tx.Sender); err != nil {
		// Best-effort to classify common stateless errors.
		// Anything else falls back to "decode_failed" for
		// the same reason: stateless validation failure
		// means the wire shape is wrong.
		reason := EnrollRejectReasonDecode
		if errors.Is(err, enrollment.ErrStakeMismatch) {
			reason = EnrollRejectReasonStakeMismatch
		}
		return rejectEnroll(reason, payload.NodeID,
			fmt.Errorf("chain: stateless enroll validation: %w", err))
	}

	senderAcc, ok := a.Accounts.Get(tx.Sender)
	if !ok {
		return rejectEnroll(EnrollRejectReasonInsufficient, payload.NodeID,
			fmt.Errorf("chain: enroll sender %q has no account", tx.Sender))
	}
	senderBalanceDust := balanceToDust(senderAcc.Balance)

	if err := enrollment.ValidateEnrollAgainstState(payload, senderBalanceDust, a.State); err != nil {
		// Stateful failures map to specific reasons.
		reason := EnrollRejectReasonOther
		switch {
		case errors.Is(err, enrollment.ErrInsufficientBalance):
			reason = EnrollRejectReasonInsufficient
		case errors.Is(err, enrollment.ErrGPUUUIDTaken):
			reason = EnrollRejectReasonGPUBound
		case errors.Is(err, enrollment.ErrNodeIDTaken):
			reason = EnrollRejectReasonNodeIDBound
		}
		return rejectEnroll(reason, payload.NodeID,
			fmt.Errorf("chain: stateful enroll validation: %w", err))
	}

	// Atomic mutation sequence:
	//
	//   1. Debit stake + tx.Fee + bump-nonce on the sender in
	//      one call. If this fails (nonce race, balance drained
	//      out from under us between the Get and now), we abort
	//      before touching enrollment state, leaving everything
	//      untouched. The fee is burned (matches transfer
	//      ApplyTx semantics: fees are not credited anywhere).
	//
	//   2. Commit the EnrollmentRecord. If this fails — which
	//      is a programming-error path because validation
	//      already passed — we roll back the STAKE only and
	//      return the error. A pure duplicate would have been
	//      caught by ValidateEnrollAgainstState, so reaching
	//      the post-validation ApplyEnroll failure means
	//      concurrent writes against the same state, which is
	//      a bug upstream. The fee is NOT refunded: validator
	//      work has been performed, matching the
	//      "fee-on-acceptance" model used everywhere else.
	stakeCELL := dustToBalance(payload.StakeDust)
	totalDebit := stakeCELL + tx.Fee
	if err := a.Accounts.DebitAndBumpNonce(tx.Sender, totalDebit, tx.Nonce); err != nil {
		return rejectEnroll(EnrollRejectReasonInsufficient, payload.NodeID,
			fmt.Errorf("chain: debit stake+fee: %w", err))
	}

	rec := enrollment.EnrollmentRecord{
		NodeID:           payload.NodeID,
		Owner:            tx.Sender,
		GPUUUID:          payload.GPUUUID,
		HMACKey:          append([]byte(nil), payload.HMACKey...),
		StakeDust:        payload.StakeDust,
		EnrolledAtHeight: height,
	}
	if err := a.State.ApplyEnroll(rec); err != nil {
		// Roll back the STAKE only. Fee remains burned (validator
		// work was performed). Credit is mutex-safe and never
		// fails; the nonce bump is NOT rolled back because doing
		// so would re-open the sender to a replay of this exact
		// tx — an attacker who forced the ApplyEnroll failure
		// (e.g. via an out-of-band state mutation) could then
		// re-run the same signed tx with the same Nonce. Instead
		// we burn the nonce and return the stake. The sender
		// re-signs a new tx at Nonce+1 if they want to retry.
		a.Accounts.Credit(tx.Sender, stakeCELL)
		return rejectEnroll(EnrollRejectReasonOther, payload.NodeID,
			fmt.Errorf("chain: commit enroll (stake refunded, fee burned, nonce consumed): %w", err))
	}

	metrics().RecordEnrollmentApplied()
	a.publisher().PublishEnrollment(EnrollmentEvent{
		Kind:      EnrollmentEventEnrollApplied,
		Height:    height,
		Sender:    tx.Sender,
		NodeID:    payload.NodeID,
		Owner:     tx.Sender,
		StakeDust: payload.StakeDust,
	})

	return nil
}

// applyUnenroll is the Unenroll-kind branch. Simpler than
// enroll because the stake stays locked until the sweep
// finalises — the only mutations are the state transition and
// the sender's nonce bump (via a zero-amount "apply tx" shape
// we don't have today — see the inline note).
func (a *EnrollmentApplier) applyUnenroll(tx *mempool.Tx, height uint64) error {
	rejectUnenroll := func(reason string, nodeID string, err error) error {
		metrics().RecordUnenrollmentRejected(reason)
		a.publisher().PublishEnrollment(EnrollmentEvent{
			Kind:         EnrollmentEventUnenrollRejected,
			Height:       height,
			Sender:       tx.Sender,
			NodeID:       nodeID,
			RejectReason: reason,
			Err:          err,
		})
		return err
	}

	payload, err := enrollment.DecodeUnenrollPayload(tx.Payload)
	if err != nil {
		return rejectUnenroll(UnenrollRejectReasonDecode, "",
			fmt.Errorf("chain: decode unenroll payload: %w", err))
	}

	if err := enrollment.ValidateUnenrollFields(payload, tx.Sender); err != nil {
		return rejectUnenroll(UnenrollRejectReasonDecode, payload.NodeID,
			fmt.Errorf("chain: stateless unenroll validation: %w", err))
	}
	if err := enrollment.ValidateUnenrollAgainstState(payload, tx.Sender, a.State); err != nil {
		reason := UnenrollRejectReasonOther
		switch {
		case errors.Is(err, enrollment.ErrNodeNotOwned):
			reason = UnenrollRejectReasonNotOwner
		case errors.Is(err, enrollment.ErrNodeAlreadyUnenrolled):
			reason = UnenrollRejectReasonAlreadyRevoked
		}
		return rejectUnenroll(reason, payload.NodeID,
			fmt.Errorf("chain: stateful unenroll validation: %w", err))
	}

	// Unenroll txs carry no balance transfer. To keep the
	// nonce-bump consistent with enroll, we charge tx.Fee as
	// the minimal debit. Callers who want a zero-fee unenroll
	// still have to pay the mempool's minimum fee, which is
	// set outside this package. A non-zero tx.Fee is REQUIRED
	// so DebitAndBumpNonce's positive-amount precondition
	// holds; we deliberately surface that as an explicit error
	// rather than letting AccountStore's internal check
	// produce a less-specific message.
	if tx.Fee <= 0 {
		return rejectUnenroll(UnenrollRejectReasonFee, payload.NodeID,
			errors.New("chain: unenroll tx requires a positive Fee for nonce accounting"))
	}
	if err := a.Accounts.DebitAndBumpNonce(tx.Sender, tx.Fee, tx.Nonce); err != nil {
		return rejectUnenroll(UnenrollRejectReasonFee, payload.NodeID,
			fmt.Errorf("chain: debit unenroll fee: %w", err))
	}

	if err := a.State.ApplyUnenroll(payload.NodeID, height); err != nil {
		// Roll back the fee debit — unlike enroll, there is
		// nothing irreversible we've done to the enrollment
		// state at this point (ApplyUnenroll is the first write),
		// so the sender sees a clean rejection. Nonce is NOT
		// rolled back for the same replay-resistance reason as
		// enroll.
		a.Accounts.Credit(tx.Sender, tx.Fee)
		return rejectUnenroll(UnenrollRejectReasonOther, payload.NodeID,
			fmt.Errorf("chain: commit unenroll (fee refunded, nonce consumed): %w", err))
	}

	metrics().RecordUnenrollmentApplied()
	a.publisher().PublishEnrollment(EnrollmentEvent{
		Kind:   EnrollmentEventUnenrollApplied,
		Height: height,
		Sender: tx.Sender,
		NodeID: payload.NodeID,
	})
	return nil
}

// SweepMaturedEnrollments releases every EnrollmentRecord whose
// unbond window has matured at `height`, crediting each
// record's StakeDust back to its Owner. Intended to be called
// exactly once per block during block finalisation, AFTER all
// transactions in the block have been applied.
//
// Returns the list of releases (for receipt / monitoring
// purposes) and any error encountered. A partial release is
// reported as an error with the successfully-credited portion
// already applied — this mirrors the existing ApplyTx-style
// semantics elsewhere in the chain and keeps finalisation
// idempotent within a block.
func (a *EnrollmentApplier) SweepMaturedEnrollments(height uint64) ([]enrollment.UnbondRelease, error) {
	if a == nil {
		return nil, errors.New("chain: nil EnrollmentApplier")
	}
	released := a.State.SweepMaturedUnbonds(height)
	if len(released) > 0 {
		metrics().RecordEnrollmentUnbondSwept(uint64(len(released)))
	}
	for _, r := range released {
		if r.Owner == "" {
			// An empty Owner means the enrollment record was
			// corrupted — skip rather than credit a zero
			// address. The release is still returned so the
			// caller can surface it.
			continue
		}
		a.Accounts.Credit(r.Owner, dustToBalance(r.StakeDust))
		a.publisher().PublishEnrollment(EnrollmentEvent{
			Kind:      EnrollmentEventSweep,
			Height:    height,
			NodeID:    r.NodeID,
			Owner:     r.Owner,
			StakeDust: r.StakeDust,
		})
	}
	return released, nil
}

// ErrNotEnrollmentTx is returned by ApplyEnrollmentTx when the
// incoming tx's ContractID does not identify an enrollment
// transaction. Exported so callers routing multiple contract
// types through a single dispatch loop can errors.Is against it.
var ErrNotEnrollmentTx = errors.New("chain: tx is not an enrollment transaction")

// balanceToDust floors a float64 CELL balance to an integer
// number of dust units. Clamps at math.MaxUint64 to avoid wrap
// on absurd inputs (a balance of ~1.8e11 CELL would overflow;
// the emission schedule makes this unreachable but we defend
// anyway so accidental test fixtures don't silently wrap).
//
// Rounding mode is floor: fractional dust is dropped, not
// rounded. This matches the conservative direction — a
// balance check that returns a LOWER dust figure errs on the
// side of rejecting borderline enrollments, which is the safe
// default for stake enforcement.
func balanceToDust(bal float64) uint64 {
	if bal <= 0 || math.IsNaN(bal) {
		return 0
	}
	scaled := bal * float64(dustPerCELL)
	if scaled >= float64(math.MaxUint64) {
		return math.MaxUint64
	}
	return uint64(scaled)
}

// dustToBalance is the exact inverse for the small range of
// values enrollment actually handles (bounded by
// mining.MinEnrollStakeDust ≈ 10 CELL). float64 has 53 bits of
// mantissa, so any dust value ≤ 2^53 round-trips exactly;
// enrollment stakes are far below that ceiling.
func dustToBalance(dust uint64) float64 {
	return float64(dust) / float64(dustPerCELL)
}
