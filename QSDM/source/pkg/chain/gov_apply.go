package chain

// gov_apply.go: consensus-layer plumbing that routes
// "qsdm/gov/v1" transactions (mempool.Tx with
// ContractID == chainparams.ContractID) through
// pkg/governance/chainparams' validation + state transitions,
// coordinated with the AccountStore (nonce + fee debit) and
// the chainparams.ParamStore (the actual consensus state).
//
// # What this is for
//
// The protocol-economy parameters that v1 burned into the
// SlashApplier struct (RewardBPS, AutoRevokeMinStakeDust) are
// now governance-tunable at runtime. A `qsdm/gov/v1` param-set
// tx, submitted by an address on the AuthorityList, stages a
// new value for activation at a future block height; the
// post-seal `Promote(height)` hook flips pending → active when
// the chain catches up.
//
// # What this is NOT for
//
//   - It is not a multisig executor. The off-chain
//     pkg/governance/multisig owns the proposal-and-signing
//     workflow; once enough signatures are collected, the
//     multisig submits a single signed gov tx via the same
//     mempool any client uses.
//
//   - It is not a wallet-balance arbiter. The fee debit goes
//     through AccountStore.DebitAndBumpNonce; a sender with
//     insufficient balance is rejected before the param store
//     is touched, identically to how SlashApplier handles
//     fee accounting.
//
//   - It is not a generic "set chain config" channel.
//     Tunable parameters are an explicit whitelist (see
//     chainparams.Registry); anything else requires a binary
//     change.

import (
	"errors"
	"fmt"
	"sort"

	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// ErrNotGovTx is returned by ApplyGovTx when the incoming tx's
// ContractID does not identify a governance transaction.
// Exported so dispatch code can errors.Is against it.
var ErrNotGovTx = errors.New("chain: tx is not a governance transaction")

// GovApplier is the chain-side adapter that bridges a
// `*mempool.Tx` carrying a chainparams payload into the
// chainparams.ParamStore. Construct via NewGovApplier; hold for
// the lifetime of the chain instance.
//
// Concurrency: ApplyGovTx is safe for concurrent use. The
// ParamStore takes its own lock; the AuthorityList is set at
// construction time and never mutated post-boot.
type GovApplier struct {
	Accounts *AccountStore
	Store    chainparams.ParamStore

	// authoritySet is a deduplicated copy of the constructor's
	// AuthorityList, indexed for O(1) tx.Sender membership
	// checks. Empty (len == 0) means governance is disabled —
	// every gov tx rejects with
	// chainparams.ErrGovernanceNotConfigured.
	authoritySet map[string]struct{}

	// Publisher receives a GovParamEvent for every gov outcome.
	// Defaults to NoopGovEventPublisher; replace with a
	// CompositeGovPublisher to fan out to indexers, audit logs,
	// CLI watchers, etc.
	Publisher GovEventPublisher
}

// NewGovApplier wires the adapter. Panics on nil Accounts /
// nil Store because both are boot-time invariants. An empty or
// nil AuthorityList is allowed and disables on-chain governance
// (every gov tx rejects with the kind-specific
// `chainparams.ErrGovernanceNotConfigured`).
//
// AuthorityList values are deduplicated; empty strings are
// silently dropped.
func NewGovApplier(
	accounts *AccountStore,
	store chainparams.ParamStore,
	authorityList []string,
) *GovApplier {
	if accounts == nil {
		panic("chain: NewGovApplier requires non-nil *AccountStore")
	}
	if store == nil {
		panic("chain: NewGovApplier requires non-nil chainparams.ParamStore")
	}
	set := make(map[string]struct{}, len(authorityList))
	for _, addr := range authorityList {
		if addr == "" {
			continue
		}
		set[addr] = struct{}{}
	}
	return &GovApplier{
		Accounts:     accounts,
		Store:        store,
		authoritySet: set,
		Publisher:    NoopGovEventPublisher{},
	}
}

// AuthorityList returns the configured authority addresses in
// ascending lexicographic order. Used by the CLI / API for
// surfacing the governance set; does NOT mutate the applier.
func (a *GovApplier) AuthorityList() []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.authoritySet))
	for addr := range a.authoritySet {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

// IsAuthority reports whether `addr` is on the AuthorityList.
// Useful for the HTTP API and tests; the applier itself uses
// the private map directly for the hot path.
func (a *GovApplier) IsAuthority(addr string) bool {
	if a == nil {
		return false
	}
	_, ok := a.authoritySet[addr]
	return ok
}

// publisher returns the configured GovEventPublisher,
// substituting NoopGovEventPublisher if the field was left nil
// (e.g. by a test that built a GovApplier struct literal
// instead of going through NewGovApplier).
func (a *GovApplier) publisher() GovEventPublisher {
	if a == nil || a.Publisher == nil {
		return NoopGovEventPublisher{}
	}
	return a.Publisher
}

// ApplyGovTx validates and applies a single gov tx at block
// `currentHeight`. Returns nil on success; on any error the
// receiver's state is untouched EXCEPT for the nonce + fee
// debit which is consumed up-front (matching the slashing /
// enrollment "fee burned on validator work" model).
//
// Order of operations:
//
//  1. Decode payload, run stateless validation.
//  2. Verify governance is configured (AuthorityList non-empty).
//  3. Verify tx.Sender is an authority.
//  4. Verify EffectiveHeight is in [currentHeight,
//     currentHeight + MaxActivationDelay].
//  5. Debit fee + bump nonce.
//  6. Stage the change in the ParamStore.
//  7. Publish event + record metrics.
func (a *GovApplier) ApplyGovTx(tx *mempool.Tx, currentHeight uint64) error {
	if a == nil {
		return errors.New("chain: nil GovApplier")
	}
	if tx == nil {
		return errors.New("chain: nil gov tx")
	}

	reject := func(reason string, ev GovParamEvent, err error) error {
		metrics().RecordGovParamRejected(reason)
		ev.Kind = GovParamEventRejected
		ev.RejectReason = reason
		ev.Err = err
		ev.Height = currentHeight
		ev.Authority = tx.Sender
		ev.TxID = tx.ID
		a.publisher().PublishGovParam(ev)
		return err
	}

	if tx.ContractID != chainparams.ContractID {
		err := fmt.Errorf("%w: got %q, want %q",
			ErrNotGovTx, tx.ContractID, chainparams.ContractID)
		return reject(GovRejectReasonWrongContract, GovParamEvent{}, err)
	}

	payload, err := chainparams.ParseParamSet(tx.Payload)
	if err != nil {
		return reject(GovRejectReasonDecode, GovParamEvent{},
			fmt.Errorf("chain: decode gov payload: %w", err))
	}
	if err := chainparams.ValidateParamSetFields(payload); err != nil {
		return reject(GovRejectReasonDecode, GovParamEvent{
			Param: payload.Param,
			Value: payload.Value,
			Memo:  payload.Memo,
		}, fmt.Errorf("chain: stateless gov validation: %w", err))
	}

	// From here, every reject path knows the param/value/memo,
	// so seed those into the event template.
	evTemplate := GovParamEvent{
		Param:           payload.Param,
		Value:           payload.Value,
		EffectiveHeight: payload.EffectiveHeight,
		Memo:            payload.Memo,
	}

	if len(a.authoritySet) == 0 {
		return reject(GovRejectReasonNotConfigured, evTemplate,
			fmt.Errorf("%w: tx.Sender=%q",
				chainparams.ErrGovernanceNotConfigured, tx.Sender))
	}
	if _, ok := a.authoritySet[tx.Sender]; !ok {
		return reject(GovRejectReasonUnauthorized, evTemplate,
			fmt.Errorf("%w: %q", chainparams.ErrUnauthorized, tx.Sender))
	}

	// Height window. The lower bound is "current height" not
	// "current height + 1" so a same-block effective height is
	// allowed (the activation is checked by Promote against
	// the height passed to it, which post-seal hooks pass as
	// the just-sealed block's height). Picking >= rather than
	// > matches the off-by-one operators expect: "set this for
	// the next block" with EffectiveHeight=currentHeight is
	// fine.
	if payload.EffectiveHeight < currentHeight {
		return reject(GovRejectReasonHeightInPast, evTemplate,
			fmt.Errorf("%w: effective_height=%d current_height=%d",
				chainparams.ErrEffectiveHeightInPast,
				payload.EffectiveHeight, currentHeight))
	}
	if payload.EffectiveHeight > currentHeight+chainparams.MaxActivationDelay {
		return reject(GovRejectReasonHeightTooFar, evTemplate,
			fmt.Errorf(
				"%w: effective_height=%d current_height=%d max_delay=%d",
				chainparams.ErrEffectiveHeightTooFar,
				payload.EffectiveHeight, currentHeight,
				chainparams.MaxActivationDelay))
	}

	// Fee + nonce. Done BEFORE state mutation so a state-side
	// failure leaves the nonce already burned, matching the
	// slashing / enrollment posture.
	if tx.Fee <= 0 {
		return reject(GovRejectReasonFee, evTemplate,
			errors.New("chain: gov tx requires a positive Fee for nonce accounting"))
	}
	if err := a.Accounts.DebitAndBumpNonce(tx.Sender, tx.Fee, tx.Nonce); err != nil {
		return reject(GovRejectReasonNonceFee, evTemplate,
			fmt.Errorf("chain: debit gov fee: %w", err))
	}

	// Stage the change. The store re-runs bounds checks
	// defensively (admission already enforced them, but a
	// programmer who builds a ParamChange by hand and skips
	// admission would slip through without this).
	change := chainparams.ParamChange{
		Param:             payload.Param,
		Value:             payload.Value,
		EffectiveHeight:   payload.EffectiveHeight,
		SubmittedAtHeight: currentHeight,
		Authority:         tx.Sender,
		Memo:              payload.Memo,
	}
	prior, hadPrior, err := a.Store.Stage(change)
	if err != nil {
		// The fee + nonce were already debited. Surface as a
		// rejection but do NOT roll back the debit (matches
		// SlashApplier's stake-mutation-failed posture).
		return reject(GovRejectReasonStageRejected, evTemplate,
			fmt.Errorf("chain: stage gov change: %w", err))
	}

	metrics().RecordGovParamStaged(payload.Param)

	// Publish: a stage event always; if a prior pending entry
	// was overwritten, also a supersede event so audit
	// consumers see the displaced change.
	if hadPrior {
		a.publisher().PublishGovParam(GovParamEvent{
			Kind:                 GovParamEventSuperseded,
			TxID:                 tx.ID,
			Height:               currentHeight,
			Authority:            tx.Sender,
			Param:                payload.Param,
			Value:                payload.Value,
			EffectiveHeight:      payload.EffectiveHeight,
			PriorValue:           prior.Value,
			PriorEffectiveHeight: prior.EffectiveHeight,
			Memo:                 payload.Memo,
		})
	}
	a.publisher().PublishGovParam(GovParamEvent{
		Kind:            GovParamEventStaged,
		TxID:            tx.ID,
		Height:          currentHeight,
		Authority:       tx.Sender,
		Param:           payload.Param,
		Value:           payload.Value,
		EffectiveHeight: payload.EffectiveHeight,
		Memo:            payload.Memo,
	})
	return nil
}

// PromotePending walks the ParamStore and activates any pending
// changes whose EffectiveHeight has been reached. Intended to
// run from the post-seal block hook (BlockProducer.OnSealedBlock)
// AFTER the block's transactions have been applied.
//
// Each promotion fires a `param-activated` event and a
// metrics-counter increment. The applier swallows ParamStore
// errors (Promote on the in-memory implementation never
// errors); callers that wire a persistent store may want a
// best-effort retry.
//
// Returns the list of promoted changes (deterministic order)
// for callers that want to log them.
func (a *GovApplier) PromotePending(currentHeight uint64) []chainparams.ParamChange {
	if a == nil || a.Store == nil {
		return nil
	}
	promoted := a.Store.Promote(currentHeight)
	for _, c := range promoted {
		metrics().RecordGovParamActivated(c.Param, c.Value)
		a.publisher().PublishGovParam(GovParamEvent{
			Kind:            GovParamEventActivated,
			Height:          currentHeight,
			Authority:       c.Authority,
			Param:           c.Param,
			Value:           c.Value,
			EffectiveHeight: c.EffectiveHeight,
			Memo:            c.Memo,
		})
	}
	return promoted
}
