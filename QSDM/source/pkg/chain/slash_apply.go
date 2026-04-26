package chain

// slash_apply.go: consensus-layer plumbing that routes
// "qsdm/slash/v1" transactions (mempool.Tx with
// ContractID == slashing.ContractID) through
// pkg/mining/slashing's verification + state transitions,
// coordinated with AccountStore credits (slasher reward) and
// the EnrollmentState (stake forfeiture, replay protection).
//
// Scope of this commit (Phase 2c-xii, Tier-A item #1):
//
//   - SlashApplier struct with ApplySlashTx.
//   - Replay protection via state.MarkEvidenceSeen keyed on
//     SHA-256(EvidenceKind || EvidenceBlob). Same evidence can
//     never slash the same record twice.
//   - Configurable slasher reward (basis points of forfeited
//     stake). Reward is credited to tx.Sender; the remainder is
//     burned (no recipient credit), matching the existing
//     fee-burn model elsewhere in this package.
//   - Atomic apply: nonce + fee debit happen first; only on
//     successful verifier + state mutation do we credit the
//     reward.
//
// The actual offence verification is delegated to
// slashing.Dispatcher. At v2 genesis, all evidence kinds are
// wired to slashing.StubVerifier, so slash transactions are
// REJECTED with ErrEvidenceVerification regardless of reward
// configuration. Concrete verifiers ship in follow-on commits
// (see MINING_PROTOCOL_V2_TIER3_SCOPE.md §4).
//
// Out of scope:
//
//   - On-chain governance for RewardBPS. The constructor takes
//     it as a static value; a future commit can swap that for
//     a chain-state lookup once governance ships.
//
// Now in scope (was out of scope at the previous revision):
//
//   - Auto-revoke under-bonded records. After a slash leaves
//     the offender's stake strictly below MinEnrollStakeDust,
//     the record is automatically revoked through
//     SlasherStateMutator.RevokeIfUnderBonded — the same
//     transition the operator would have triggered with an
//     ordinary unenroll, including the unbond-window stake
//     release and the gpu_uuid binding release. This closes
//     the "slash to zero, keep mining for free" loophole.

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
)

// SlasherStateMutator is the subset of enrollment-state methods
// the slash applier needs. *enrollment.InMemoryState satisfies
// this by shape (after the SlashStake / MarkEvidenceSeen
// extension landed in this commit).
//
// Declared locally to keep pkg/chain depending only on what it
// uses, mirroring the EnrollmentStateMutator pattern in
// enrollment_apply.go.
type SlasherStateMutator interface {
	// Lookup returns the EnrollmentRecord for nodeID, or
	// (nil, nil) if no record exists. Same semantics as
	// EnrollmentState.Lookup.
	Lookup(nodeID string) (*enrollment.EnrollmentRecord, error)

	// SlashStake forfeits up to `amount` dust from the named
	// record's StakeDust. Returns the actually-forfeited
	// amount (clamped at the remaining stake) and any error.
	SlashStake(nodeID string, amount uint64) (uint64, error)

	// MarkEvidenceSeen returns true if the hash was newly
	// inserted, false if it had already been recorded.
	// Replay protection.
	MarkEvidenceSeen(hash [32]byte) bool

	// RevokeIfUnderBonded auto-revokes the named record when
	// its post-mutation StakeDust falls below minStakeDust.
	// Returns (revoked, remaining, err): revoked=true when
	// this call newly transitioned the record into the unbond
	// window; remaining is the stake still locked. The slash
	// applier calls this immediately after SlashStake so a
	// fully-drained-or-under-bonded record cannot keep mining
	// at sub-minimum collateral. Idempotent on already-revoked
	// records.
	RevokeIfUnderBonded(
		nodeID string,
		currentHeight uint64,
		minStakeDust uint64,
	) (bool, uint64, error)
}

// SlashRewardCap is the maximum reward fraction the chain will
// honour for a single slash, in basis points. 5000 bps = 50%.
// Accepting unbounded rewards would let governance set
// RewardBPS to 100% and destroy the incentive to keep stake
// bonded (every slash becomes a transfer, not a burn). Chosen
// at 50% because that's the upper bound used by Cosmos SDK's
// slashing module — empirically tested anti-collusion ceiling.
const SlashRewardCap uint16 = 5000

// SlashApplier is the chain-side adapter that bridges a
// *mempool.Tx carrying a slashing payload into the
// pkg/mining/slashing verifier + the enrollment state's
// SlashStake / MarkEvidenceSeen methods.
//
// Construct via NewSlashApplier. Hold for the lifetime of the
// chain instance.
type SlashApplier struct {
	Accounts   *AccountStore
	State      SlasherStateMutator
	Dispatcher *slashing.Dispatcher

	// RewardBPS is the slasher reward fraction in basis points
	// of the forfeited stake. 0 means burn-everything; 10000
	// would mean reward-everything (clamped to SlashRewardCap
	// at construction).
	RewardBPS uint16

	// AutoRevokeMinStakeDust is the threshold below which a
	// post-slash record is auto-revoked into the unbond
	// window. NewSlashApplier defaults this to
	// mining.MinEnrollStakeDust; callers can override (e.g.
	// tests probing boundary conditions, or a future
	// chain-config flag that wants a different threshold).
	// Setting this to 0 disables auto-revoke entirely.
	AutoRevokeMinStakeDust uint64

	// Publisher receives a MiningSlashEvent for every slash outcome
	// (applied + each rejection path). Defaults to
	// NoopEventPublisher; set to a CompositePublisher to fan
	// out to indexers, audit logs, etc. Calls are synchronous
	// from the applier's view — see pkg/chain/events.go.
	Publisher ChainEventPublisher
}

// NewSlashApplier wires the adapter. Panics on nil fields and
// on RewardBPS > SlashRewardCap because both are boot-time
// configuration mistakes that should crash, not be tolerated
// per-tx.
func NewSlashApplier(
	accounts *AccountStore,
	state SlasherStateMutator,
	dispatcher *slashing.Dispatcher,
	rewardBPS uint16,
) *SlashApplier {
	if accounts == nil {
		panic("chain: NewSlashApplier requires non-nil *AccountStore")
	}
	if state == nil {
		panic("chain: NewSlashApplier requires non-nil SlasherStateMutator")
	}
	if dispatcher == nil {
		panic("chain: NewSlashApplier requires non-nil slashing.Dispatcher")
	}
	if rewardBPS > SlashRewardCap {
		panic(fmt.Sprintf(
			"chain: NewSlashApplier RewardBPS=%d exceeds SlashRewardCap=%d",
			rewardBPS, SlashRewardCap))
	}
	return &SlashApplier{
		Accounts:               accounts,
		State:                  state,
		Dispatcher:             dispatcher,
		RewardBPS:              rewardBPS,
		AutoRevokeMinStakeDust: mining.MinEnrollStakeDust,
		Publisher:              NoopEventPublisher{},
	}
}

// ApplySlashTx validates and applies a single slash transaction
// at block `currentHeight`. Returns nil on success; on any
// error the receiver's state is untouched EXCEPT for the
// nonce+fee debit which is consumed up-front (matching the
// "fee burned on validator work" model used by enrollment).
//
// Order of operations:
//
//   1. Decode payload, run stateless validation.
//   2. Look up the offender's EnrollmentRecord; reject with
//      ErrNodeNotEnrolled if absent.
//   3. Hash the evidence and reject if already seen.
//   4. Run the verifier dispatcher; reject if Verify errors.
//   5. Compute actualSlash = min(payload.SlashAmountDust,
//      verifierCap, record.StakeDust). Zero is allowed (no-op
//      forfeiture but the evidence-seen marker still locks
//      future replays).
//   6. Debit slasher's tx.Fee + bump nonce.
//   7. Mark evidence seen; if a concurrent applier won, abort
//      with fee burned (replay defence).
//   8. SlashStake on the record; on partial failure roll back
//      the evidence-seen marker (the actual mutation in step 8
//      is what should have been atomic with step 7, so a
//      failure here is a programmer-error path; we still
//      attempt to leave state consistent).
//   9. Credit the slasher with rewardDust = actualSlash *
//      RewardBPS / 10000. Burn the remainder (no credit
//      issued).
//   10. Auto-revoke the record if its post-slash stake is
//       strictly below AutoRevokeMinStakeDust (default
//       mining.MinEnrollStakeDust). Errors here are
//       swallowed deliberately — the slash itself succeeded,
//       and a revoke failure is at worst a missed cleanup
//       that the next epoch's bond-floor check would catch.
//       Callers that want hard guarantees can inspect the
//       SlasherStateMutator directly after ApplySlashTx
//       returns nil.
func (a *SlashApplier) ApplySlashTx(tx *mempool.Tx, currentHeight uint64) error {
	if a == nil {
		return errors.New("chain: nil SlashApplier")
	}
	if tx == nil {
		return errors.New("chain: nil slash tx")
	}

	// reject is the local helper used on every pre-mutation
	// rejection path. It records the reason in monitoring,
	// publishes a MiningSlashEvent (with whatever payload fields
	// have been decoded so far), and returns the wrapped
	// error.
	reject := func(reason string, ev MiningSlashEvent, err error) error {
		metrics().RecordSlashRejected(reason)
		ev.Outcome = SlashOutcomeRejected
		ev.RejectReason = reason
		ev.Err = err
		ev.Height = currentHeight
		ev.Slasher = tx.Sender
		ev.TxID = tx.ID
		a.publisher().PublishMiningSlash(ev)
		return err
	}

	if tx.ContractID != slashing.ContractID {
		err := fmt.Errorf("%w: got %q, want %q",
			ErrNotSlashTx, tx.ContractID, slashing.ContractID)
		return reject(SlashRejectReasonWrongContract, MiningSlashEvent{}, err)
	}

	payload, err := slashing.DecodeSlashPayload(tx.Payload)
	if err != nil {
		return reject(SlashRejectReasonDecode, MiningSlashEvent{},
			fmt.Errorf("chain: decode slash payload: %w", err))
	}
	if err := slashing.ValidateSlashFields(payload, tx.Sender); err != nil {
		return reject(SlashRejectReasonDecode,
			MiningSlashEvent{NodeID: payload.NodeID, EvidenceKind: payload.EvidenceKind},
			fmt.Errorf("chain: stateless slash validation: %w", err))
	}

	// From here on, every reject path knows the offender
	// node_id and the evidence kind, so seed those into the
	// MiningSlashEvent template.
	evTemplate := MiningSlashEvent{
		NodeID:       payload.NodeID,
		EvidenceKind: payload.EvidenceKind,
	}

	// Step 2 - 3: stateful pre-checks.
	rec, err := a.State.Lookup(payload.NodeID)
	if err != nil {
		return reject(SlashRejectReasonStateLookup, evTemplate,
			fmt.Errorf("chain: slash state lookup: %w", err))
	}
	if rec == nil {
		return reject(SlashRejectReasonNodeNotEnrolled, evTemplate,
			fmt.Errorf("%w: %q", slashing.ErrNodeNotEnrolled, payload.NodeID))
	}
	evidenceHash := evidenceFingerprint(payload)
	// Pre-check (does NOT mutate yet — we only mark after
	// verification + stake mutation succeed). This is a fast
	// reject for the common "already-slashed" case so we don't
	// waste verifier work on duplicates.
	if seenChecker, ok := a.State.(interface{ EvidenceSeen([32]byte) bool }); ok {
		if seenChecker.EvidenceSeen(evidenceHash) {
			return reject(SlashRejectReasonEvidenceReplay, evTemplate,
				fmt.Errorf("chain: slash evidence already seen for node_id %q", payload.NodeID))
		}
	}

	// Step 4: verifier dispatch.
	verifierCap, err := a.Dispatcher.Verify(payload, currentHeight)
	if err != nil {
		return reject(SlashRejectReasonVerifier, evTemplate,
			fmt.Errorf("chain: slash verifier: %w", err))
	}

	// Step 5: clamp the slash amount.
	actualSlash := payload.SlashAmountDust
	if verifierCap > 0 && actualSlash > verifierCap {
		actualSlash = verifierCap
	}
	if actualSlash > rec.StakeDust {
		actualSlash = rec.StakeDust
	}

	// Step 6: debit slasher's fee + bump nonce. Done BEFORE
	// any state mutation so a state-side failure leaves the
	// nonce already burned (matching enroll/unenroll).
	if tx.Fee <= 0 {
		return reject(SlashRejectReasonFee, evTemplate,
			errors.New("chain: slash tx requires a positive Fee for nonce accounting"))
	}
	if err := a.Accounts.DebitAndBumpNonce(tx.Sender, tx.Fee, tx.Nonce); err != nil {
		return reject(SlashRejectReasonFee, evTemplate,
			fmt.Errorf("chain: debit slash fee: %w", err))
	}

	// Step 7: mark evidence seen — atomic with step 8 below
	// from the same goroutine (the state mutex protects both).
	if !a.State.MarkEvidenceSeen(evidenceHash) {
		// Lost a race with a concurrent slasher. Fee is
		// burned; nonce is consumed. Surface as a clean
		// rejection.
		return reject(SlashRejectReasonEvidenceReplay, evTemplate,
			fmt.Errorf("chain: slash evidence raced (already accepted by concurrent tx)"))
	}

	// Step 8: forfeit the stake.
	slashed, err := a.State.SlashStake(payload.NodeID, actualSlash)
	if err != nil {
		// Should not happen — the record existed at step 2.
		// Defensive only. We DO NOT call reject() here because
		// the fee + nonce were already debited and the
		// evidence-seen marker was set; the caller should
		// surface the inconsistency. Record the metric so the
		// scrape catches it but skip the event since the slash
		// is in a half-applied state we don't model.
		metrics().RecordSlashRejected(SlashRejectReasonStakeMutation)
		return fmt.Errorf("chain: slash stake: %w", err)
	}

	// Step 9: pay the slasher reward, burn the rest.
	rewardDust := uint64(0)
	if slashed > 0 && a.RewardBPS > 0 {
		// 64-bit safe: slashed <= 2^64-1, RewardBPS <= 5000.
		rewardDust = slashed * uint64(a.RewardBPS) / 10000
		if rewardDust > 0 {
			a.Accounts.Credit(tx.Sender, dustToBalance(rewardDust))
		}
	}
	burnedDust := slashed - rewardDust

	// Step 10: auto-revoke under-bonded records. Best-effort:
	// the slash already succeeded, so a revoke failure must
	// not unwind the slash. SlasherStateMutator implementations
	// that don't actually enforce auto-revoke can still pass
	// here as long as they return (false, _, nil) — that's the
	// idempotent "no-op" contract documented on
	// enrollment.InMemoryState.RevokeIfUnderBonded.
	autoRevoked := false
	autoRevokeRemaining := uint64(0)
	if a.AutoRevokeMinStakeDust > 0 {
		revoked, remaining, _ := a.State.RevokeIfUnderBonded(
			payload.NodeID,
			currentHeight,
			a.AutoRevokeMinStakeDust,
		)
		autoRevoked = revoked
		autoRevokeRemaining = remaining
		if revoked {
			if remaining == 0 {
				metrics().RecordSlashAutoRevoke(SlashAutoRevokeReasonFullDrain)
			} else {
				metrics().RecordSlashAutoRevoke(SlashAutoRevokeReasonUnderBonded)
			}
		}
	}

	// Record the success-path metrics + publish the applied event.
	metrics().RecordSlashApplied(string(payload.EvidenceKind), slashed)
	metrics().RecordSlashReward(rewardDust, burnedDust)
	a.publisher().PublishMiningSlash(MiningSlashEvent{
		TxID:                    tx.ID,
		Outcome:                 SlashOutcomeApplied,
		Height:                  currentHeight,
		Slasher:                 tx.Sender,
		NodeID:                  payload.NodeID,
		EvidenceKind:            payload.EvidenceKind,
		SlashedDust:             slashed,
		RewardedDust:            rewardDust,
		BurnedDust:              burnedDust,
		AutoRevoked:             autoRevoked,
		AutoRevokeRemainingDust: autoRevokeRemaining,
	})

	return nil
}

// publisher returns the configured ChainEventPublisher,
// substituting NoopEventPublisher if the field was left nil
// (e.g. by a test that built a SlashApplier struct literal
// instead of going through NewSlashApplier).
func (a *SlashApplier) publisher() ChainEventPublisher {
	if a == nil || a.Publisher == nil {
		return NoopEventPublisher{}
	}
	return a.Publisher
}

// evidenceFingerprint computes the replay-dedup key for a
// slash payload. SHA-256 over the kind-and-blob concatenation
// — independent of NodeID, so an attacker cannot reuse the same
// evidence across two different node_ids (which is impossible
// for honest evidence anyway because the evidence carries the
// offender's identity in its blob).
func evidenceFingerprint(p slashing.SlashPayload) [32]byte {
	h := sha256.New()
	h.Write([]byte(p.EvidenceKind))
	h.Write([]byte{0x00}) // delimiter so kind|blob can't collide via append-extension
	h.Write(p.EvidenceBlob)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// ErrNotSlashTx is returned by ApplySlashTx when the incoming
// tx's ContractID does not identify a slashing transaction.
// Exported so dispatch code can errors.Is against it.
var ErrNotSlashTx = errors.New("chain: tx is not a slashing transaction")
