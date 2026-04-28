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
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// ErrNotGovTx is returned by ApplyGovTx when the incoming tx's
// ContractID does not identify a governance transaction.
// Exported so dispatch code can errors.Is against it.
var ErrNotGovTx = errors.New("chain: tx is not a governance transaction")

// GovApplier is the chain-side adapter that bridges a
// `*mempool.Tx` carrying a chainparams payload into the
// chainparams.ParamStore + chainparams.AuthorityVoteStore.
// Construct via NewGovApplier; hold for the lifetime of the
// chain instance.
//
// Concurrency: ApplyGovTx is safe for concurrent use. The
// ParamStore and AuthorityVoteStore take their own locks; the
// authoritySet is held behind authorityMu (RWMutex) so the
// post-promotion add/remove mutations and IsAuthority reads
// don't race.
type GovApplier struct {
	Accounts *AccountStore
	Store    chainparams.ParamStore

	// AuthorityVotes tallies authority-rotation votes.
	// Optional — if nil, `qsdm/gov/v1` authority-set txs
	// reject with the kind-specific
	// chainparams.ErrGovernanceNotConfigured. New deployments
	// always wire a non-nil store; the optionality is for
	// pre-rotation chains that haven't yet bumped their
	// snapshot format. Set via NewGovApplier or
	// SetAuthorityVoteStore.
	AuthorityVotes chainparams.AuthorityVoteStore

	// authorityMu guards authoritySet. Held for write during
	// Promote-driven add/remove activations; held for read on
	// the hot path (ApplyGovTx authority membership checks).
	authorityMu sync.RWMutex

	// authoritySet is a deduplicated copy of the constructor's
	// AuthorityList, indexed for O(1) tx.Sender membership
	// checks. Mutated post-boot ONLY by Promote-driven
	// authority-rotation activations under authorityMu. Empty
	// (len == 0) means governance is disabled — every gov tx
	// rejects with chainparams.ErrGovernanceNotConfigured.
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

// SetAuthorityVoteStore wires (or replaces) the per-applier
// authority-rotation vote store. Safe to call post-construction;
// in production v2wiring sets it via the constructor. A nil
// store reverts to "authority rotation disabled" — `qsdm/gov/v1`
// authority-set txs reject with ErrGovernanceNotConfigured.
func (a *GovApplier) SetAuthorityVoteStore(s chainparams.AuthorityVoteStore) {
	if a == nil {
		return
	}
	a.AuthorityVotes = s
}

// AuthorityList returns the configured authority addresses in
// ascending lexicographic order. Used by the CLI / API for
// surfacing the governance set; does NOT mutate the applier.
//
// Held behind authorityMu's read lock so a concurrent
// Promote-driven mutation can't tear the snapshot.
func (a *GovApplier) AuthorityList() []string {
	if a == nil {
		return nil
	}
	a.authorityMu.RLock()
	defer a.authorityMu.RUnlock()
	out := make([]string, 0, len(a.authoritySet))
	for addr := range a.authoritySet {
		out = append(out, addr)
	}
	sort.Strings(out)
	return out
}

// authorityCount returns the live AuthorityList size under
// authorityMu's read lock. Internal helper for the threshold
// computation in ApplyAuthoritySet.
func (a *GovApplier) authorityCount() int {
	a.authorityMu.RLock()
	defer a.authorityMu.RUnlock()
	return len(a.authoritySet)
}

// IsAuthority reports whether `addr` is on the AuthorityList.
// Useful for the HTTP API and tests; the applier itself uses
// the private map directly for the hot path (still under
// authorityMu's read lock).
func (a *GovApplier) IsAuthority(addr string) bool {
	if a == nil {
		return false
	}
	a.authorityMu.RLock()
	defer a.authorityMu.RUnlock()
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
// Dispatches on PayloadKind:
//
//   - PayloadKindParamSet     → applyParamSet (the v1 surface)
//   - PayloadKindAuthoritySet → applyAuthoritySet (M-of-N rotation)
//
// Both share the wrong-contract / decode-failure prologue
// because the framing is identical; the kind tag is the
// dispatch discriminator.
func (a *GovApplier) ApplyGovTx(tx *mempool.Tx, currentHeight uint64) error {
	if a == nil {
		return errors.New("chain: nil GovApplier")
	}
	if tx == nil {
		return errors.New("chain: nil gov tx")
	}
	if tx.ContractID != chainparams.ContractID {
		// Wrong-contract reject is shape-agnostic — the
		// payload may not even be parseable. Emit a bare
		// param-rejected event (the historical behaviour the
		// audit trail expects) and surface a wrong-contract
		// metric on the param-rejected counter.
		err := fmt.Errorf("%w: got %q, want %q",
			ErrNotGovTx, tx.ContractID, chainparams.ContractID)
		metrics().RecordGovParamRejected(GovRejectReasonWrongContract)
		a.publisher().PublishGovParam(GovParamEvent{
			Kind:         GovParamEventRejected,
			TxID:         tx.ID,
			Height:       currentHeight,
			Authority:    tx.Sender,
			RejectReason: GovRejectReasonWrongContract,
			Err:          err,
		})
		return err
	}

	kind, err := chainparams.PeekKind(tx.Payload)
	if err != nil {
		// A decode failure at the kind-peek stage is
		// shape-ambiguous — emit it as a param-rejected
		// event (the v1 historical surface).
		metrics().RecordGovParamRejected(GovRejectReasonDecode)
		wrap := fmt.Errorf("chain: decode gov payload: %w", err)
		a.publisher().PublishGovParam(GovParamEvent{
			Kind:         GovParamEventRejected,
			TxID:         tx.ID,
			Height:       currentHeight,
			Authority:    tx.Sender,
			RejectReason: GovRejectReasonDecode,
			Err:          wrap,
		})
		return wrap
	}

	switch kind {
	case chainparams.PayloadKindParamSet:
		return a.applyParamSet(tx, currentHeight)
	case chainparams.PayloadKindAuthoritySet:
		return a.applyAuthoritySet(tx, currentHeight)
	default:
		// PeekKind already filters unknown values; this
		// branch is unreachable but kept defensively so a
		// future PayloadKind addition that forgets to
		// extend the switch is loud rather than silent.
		err := fmt.Errorf(
			"%w: unsupported gov payload kind %q",
			chainparams.ErrPayloadInvalid, kind)
		metrics().RecordGovParamRejected(GovRejectReasonDecode)
		a.publisher().PublishGovParam(GovParamEvent{
			Kind:         GovParamEventRejected,
			TxID:         tx.ID,
			Height:       currentHeight,
			Authority:    tx.Sender,
			RejectReason: GovRejectReasonDecode,
			Err:          err,
		})
		return err
	}
}

// applyParamSet applies the v1 `param-set` payload — the
// pre-rotation surface. Hot-path identical to the previous
// ApplyGovTx body; split out to make the kind dispatch
// readable.
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
func (a *GovApplier) applyParamSet(tx *mempool.Tx, currentHeight uint64) error {
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

	a.authorityMu.RLock()
	n := len(a.authoritySet)
	_, isAuth := a.authoritySet[tx.Sender]
	a.authorityMu.RUnlock()
	if n == 0 {
		return reject(GovRejectReasonNotConfigured, evTemplate,
			fmt.Errorf("%w: tx.Sender=%q",
				chainparams.ErrGovernanceNotConfigured, tx.Sender))
	}
	if !isAuth {
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
// changes whose EffectiveHeight has been reached, then walks
// the AuthorityVoteStore and activates any Crossed authority
// proposals whose EffectiveHeight has been reached. Intended to
// run from the post-seal block hook (BlockProducer.OnSealedBlock)
// AFTER the block's transactions have been applied.
//
// Each parameter promotion fires a `param-activated` event and
// a metrics-counter increment. Each authority promotion fires
// an `authority-activated` event, mutates the authoritySet
// behind authorityMu, and (for removes) drops orphaned votes.
//
// Returns the list of promoted parameter changes (deterministic
// order) for callers that want to log them. Authority
// promotions are surfaced via the publisher; callers that need
// the full record can inspect the publisher.
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
	a.promoteAuthorityPending(currentHeight)
	return promoted
}

// applyAuthoritySet handles one `authority-set` vote tx. The
// flow is structurally similar to applyParamSet: shared
// stateless validation, fee+nonce debit before state mutation,
// then a vote-record step that may flip the proposal to
// Crossed.
//
// Differences from param-set:
//
//   - Op-specific membership checks: add must NOT be present;
//     remove must BE present. Stops obvious wasted votes
//     before the fee burn.
//
//   - Vote-store mutation publishes a separate event family
//     (GovAuthorityEvent), and the threshold-cross path
//     publishes BOTH `authority-voted` (always) and
//     `authority-staged` (only when the new vote crossed).
//
//   - No "supersede" event: each vote tuple is independent;
//     there is no per-(op,address) "current pending" slot.
//     Multiple parallel proposals on the same address are
//     allowed and are tallied independently.
func (a *GovApplier) applyAuthoritySet(tx *mempool.Tx, currentHeight uint64) error {
	reject := func(reason string, ev GovAuthorityEvent, err error) error {
		metrics().RecordGovAuthorityRejected(reason)
		ev.Kind = GovAuthorityEventRejected
		ev.RejectReason = reason
		ev.Err = err
		ev.Height = currentHeight
		ev.Voter = tx.Sender
		ev.TxID = tx.ID
		a.publisher().PublishGovAuthority(ev)
		return err
	}
	// Some classes of rejection should still flow through the
	// param-rejected metric (decode_failed, unauthorized,
	// height window, fee, not_configured) because those are
	// shape-agnostic and existing dashboards already track
	// them on the param-rejected counter.
	rejectShared := func(paramReason, authReason string, ev GovAuthorityEvent, err error) error {
		metrics().RecordGovParamRejected(paramReason)
		return reject(authReason, ev, err)
	}

	payload, err := chainparams.ParseAuthoritySet(tx.Payload)
	if err != nil {
		return rejectShared(GovRejectReasonDecode, GovRejectReasonAuthorityVoteRejected,
			GovAuthorityEvent{}, fmt.Errorf("chain: decode authority payload: %w", err))
	}
	if err := chainparams.ValidateAuthoritySetFields(payload); err != nil {
		return rejectShared(GovRejectReasonDecode, GovRejectReasonAuthorityVoteRejected,
			GovAuthorityEvent{
				Op:              string(payload.Op),
				Address:         payload.Address,
				EffectiveHeight: payload.EffectiveHeight,
				Memo:            payload.Memo,
			}, fmt.Errorf("chain: stateless authority validation: %w", err))
	}

	evTemplate := GovAuthorityEvent{
		Op:              string(payload.Op),
		Address:         payload.Address,
		EffectiveHeight: payload.EffectiveHeight,
		Memo:            payload.Memo,
	}

	a.authorityMu.RLock()
	n := len(a.authoritySet)
	_, isAuth := a.authoritySet[tx.Sender]
	_, targetPresent := a.authoritySet[payload.Address]
	a.authorityMu.RUnlock()
	evTemplate.AuthorityCount = n
	evTemplate.Threshold = chainparams.AuthorityThreshold(n)

	if n == 0 || a.AuthorityVotes == nil {
		return rejectShared(GovRejectReasonNotConfigured, GovRejectReasonAuthorityVoteRejected,
			evTemplate, fmt.Errorf("%w: tx.Sender=%q",
				chainparams.ErrGovernanceNotConfigured, tx.Sender))
	}
	if !isAuth {
		return rejectShared(GovRejectReasonUnauthorized, GovRejectReasonAuthorityVoteRejected,
			evTemplate, fmt.Errorf("%w: %q", chainparams.ErrUnauthorized, tx.Sender))
	}

	switch payload.Op {
	case chainparams.AuthorityOpAdd:
		if targetPresent {
			return reject(GovRejectReasonAuthorityAlreadyPresent, evTemplate,
				fmt.Errorf("%w: address=%q",
					chainparams.ErrAuthorityAlreadyPresent, payload.Address))
		}
	case chainparams.AuthorityOpRemove:
		if !targetPresent {
			return reject(GovRejectReasonAuthorityNotPresent, evTemplate,
				fmt.Errorf("%w: address=%q",
					chainparams.ErrAuthorityNotPresent, payload.Address))
		}
	}

	if payload.EffectiveHeight < currentHeight {
		return rejectShared(GovRejectReasonHeightInPast, GovRejectReasonAuthorityVoteRejected,
			evTemplate, fmt.Errorf("%w: effective_height=%d current_height=%d",
				chainparams.ErrEffectiveHeightInPast,
				payload.EffectiveHeight, currentHeight))
	}
	if payload.EffectiveHeight > currentHeight+chainparams.MaxActivationDelay {
		return rejectShared(GovRejectReasonHeightTooFar, GovRejectReasonAuthorityVoteRejected,
			evTemplate, fmt.Errorf(
				"%w: effective_height=%d current_height=%d max_delay=%d",
				chainparams.ErrEffectiveHeightTooFar,
				payload.EffectiveHeight, currentHeight,
				chainparams.MaxActivationDelay))
	}

	if tx.Fee <= 0 {
		return rejectShared(GovRejectReasonFee, GovRejectReasonAuthorityVoteRejected,
			evTemplate, errors.New(
				"chain: gov tx requires a positive Fee for nonce accounting"))
	}
	if err := a.Accounts.DebitAndBumpNonce(tx.Sender, tx.Fee, tx.Nonce); err != nil {
		return rejectShared(GovRejectReasonNonceFee, GovRejectReasonAuthorityVoteRejected,
			evTemplate, fmt.Errorf("chain: debit authority vote fee: %w", err))
	}

	key := chainparams.AuthorityVoteKey{
		Op:              payload.Op,
		Address:         payload.Address,
		EffectiveHeight: payload.EffectiveHeight,
	}
	vote := chainparams.AuthorityVote{
		Voter:             tx.Sender,
		SubmittedAtHeight: currentHeight,
		Memo:              payload.Memo,
	}
	prop, crossedNow, err := a.AuthorityVotes.RecordVote(key, vote, n)
	if err != nil {
		// Fee already burned, mirror the param-set posture.
		if errors.Is(err, chainparams.ErrDuplicateVote) {
			return reject(GovRejectReasonDuplicateVote, evTemplate, err)
		}
		return reject(GovRejectReasonAuthorityVoteRejected, evTemplate,
			fmt.Errorf("chain: record authority vote: %w", err))
	}

	metrics().RecordGovAuthorityVoted(string(payload.Op))
	voters := voterAddrs(prop.Voters)

	a.publisher().PublishGovAuthority(GovAuthorityEvent{
		Kind:            GovAuthorityEventVoted,
		TxID:            tx.ID,
		Height:          currentHeight,
		Voter:           tx.Sender,
		Op:              string(payload.Op),
		Address:         payload.Address,
		EffectiveHeight: payload.EffectiveHeight,
		Voters:          voters,
		Threshold:       evTemplate.Threshold,
		AuthorityCount:  n,
		Memo:            payload.Memo,
	})
	if crossedNow {
		metrics().RecordGovAuthorityCrossed(string(payload.Op))
		a.publisher().PublishGovAuthority(GovAuthorityEvent{
			Kind:            GovAuthorityEventStaged,
			TxID:            tx.ID,
			Height:          currentHeight,
			Voter:           tx.Sender,
			Op:              string(payload.Op),
			Address:         payload.Address,
			EffectiveHeight: payload.EffectiveHeight,
			Voters:          voters,
			Threshold:       evTemplate.Threshold,
			AuthorityCount:  n,
			Memo:            payload.Memo,
		})
	}
	return nil
}

// promoteAuthorityPending activates every Crossed authority
// proposal whose EffectiveHeight has been reached, mutates
// the authoritySet under authorityMu, and (for removes) drops
// the removed authority's votes from any open proposals.
//
// Mutations are published as `authority-activated` events.
// A remove that would empty the AuthorityList is REFUSED at
// promotion time (a `authority-rejected` event with reason
// `authority_would_empty` is emitted; the proposal is deleted
// because re-voting it would bypass the same guard at the
// next promotion). Operators must redeploy binaries to
// disable governance from on-chain.
//
// Activation order is deterministic: by EffectiveHeight asc,
// Op asc ("add" before "remove" within the same height; this
// matters when a proposal pair simultaneously rotates one
// authority for another, because the add lands first and
// cushions the threshold for any subsequent removes), Address
// asc.
func (a *GovApplier) promoteAuthorityPending(currentHeight uint64) {
	if a == nil || a.AuthorityVotes == nil {
		return
	}
	due := a.AuthorityVotes.Promote(currentHeight)
	if len(due) == 0 {
		return
	}

	a.authorityMu.Lock()
	for i := range due {
		prop := due[i]
		switch prop.Op {
		case chainparams.AuthorityOpAdd:
			a.authoritySet[prop.Address] = struct{}{}
		case chainparams.AuthorityOpRemove:
			if len(a.authoritySet) <= 1 {
				// Refuse the activation; emit the
				// rejection event after we drop the
				// authorityMu lock.
				due[i].Voters = nil // sentinel for the post-loop pass
				continue
			}
			delete(a.authoritySet, prop.Address)
		}
	}
	postCount := len(a.authoritySet)
	a.authorityMu.Unlock()

	for _, prop := range due {
		// Republish authority-rotation events outside the
		// authorityMu critical section so subscribers can
		// inspect the applier without deadlocking.
		if prop.Op == chainparams.AuthorityOpRemove && prop.Voters == nil {
			metrics().RecordGovAuthorityRejected(GovRejectReasonAuthorityWouldEmpty)
			a.publisher().PublishGovAuthority(GovAuthorityEvent{
				Kind:            GovAuthorityEventRejected,
				Height:          currentHeight,
				Op:              string(prop.Op),
				Address:         prop.Address,
				EffectiveHeight: prop.EffectiveHeight,
				AuthorityCount:  postCount,
				Threshold:       chainparams.AuthorityThreshold(postCount),
				RejectReason:    GovRejectReasonAuthorityWouldEmpty,
				Err:             chainparams.ErrAuthorityListWouldEmpty,
			})
			continue
		}
		metrics().RecordGovAuthorityActivated(string(prop.Op), uint64(postCount))
		a.publisher().PublishGovAuthority(GovAuthorityEvent{
			Kind:            GovAuthorityEventActivated,
			Height:          currentHeight,
			Op:              string(prop.Op),
			Address:         prop.Address,
			EffectiveHeight: prop.EffectiveHeight,
			Voters:          voterAddrs(prop.Voters),
			Threshold:       chainparams.AuthorityThreshold(postCount),
			AuthorityCount:  postCount,
			Memo:            firstNonEmptyMemo(prop.Voters),
		})
		// On a successful remove, drop that authority's
		// votes from every open proposal and re-evaluate
		// whether any of those crossed under the smaller
		// threshold.
		if prop.Op == chainparams.AuthorityOpRemove {
			abandoned := a.AuthorityVotes.DropVotesByAuthority(prop.Address)
			for _, ap := range abandoned {
				if len(ap.Voters) == 0 {
					a.publisher().PublishGovAuthority(GovAuthorityEvent{
						Kind:            GovAuthorityEventAbandoned,
						Height:          currentHeight,
						Op:              string(ap.Op),
						Address:         ap.Address,
						EffectiveHeight: ap.EffectiveHeight,
						AuthorityCount:  postCount,
						Threshold:       chainparams.AuthorityThreshold(postCount),
					})
				}
			}
			newlyCrossed := a.AuthorityVotes.RecomputeCrossed(postCount, currentHeight)
			for _, nc := range newlyCrossed {
				metrics().RecordGovAuthorityCrossed(string(nc.Op))
				a.publisher().PublishGovAuthority(GovAuthorityEvent{
					Kind:            GovAuthorityEventStaged,
					Height:          currentHeight,
					Op:              string(nc.Op),
					Address:         nc.Address,
					EffectiveHeight: nc.EffectiveHeight,
					Voters:          voterAddrs(nc.Voters),
					Threshold:       chainparams.AuthorityThreshold(postCount),
					AuthorityCount:  postCount,
				})
			}
		}
	}
}

// voterAddrs flattens a []AuthorityVote to a []string of voter
// addresses, preserving the deterministic order the store
// applies (SubmittedAtHeight asc, voter asc).
func voterAddrs(votes []chainparams.AuthorityVote) []string {
	if len(votes) == 0 {
		return nil
	}
	out := make([]string, len(votes))
	for i, v := range votes {
		out[i] = v.Voter
	}
	return out
}

// firstNonEmptyMemo returns the first non-empty Memo across
// the proposal's Voters, ordered. Used as the activation
// event's Memo so dashboards see a representative reason
// even if not every voter supplied one.
func firstNonEmptyMemo(votes []chainparams.AuthorityVote) string {
	for _, v := range votes {
		if v.Memo != "" {
			return v.Memo
		}
	}
	return ""
}
