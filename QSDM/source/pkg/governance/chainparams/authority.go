package chainparams

// authority.go — vote-tally store for `qsdm/gov/v1` authority
// rotation proposals (Kind = PayloadKindAuthoritySet).
//
// # Why this is its own store
//
// ParamStore tracks at-most-one pending change per parameter
// keyed by name. AuthorityVoteStore tracks proposals keyed by
// the (Op, Address, EffectiveHeight) tuple, and each proposal
// carries a per-voter set rather than a single value. The
// shapes are different enough that wedging them into the same
// interface would force every reader to switch on a payload
// kind anyway. Two stores, one persistence file, no shared
// abstraction beyond "promote at effective height".
//
// # Threshold semantics
//
// Threshold is computed at vote-application time from the live
// AuthorityList size:
//
//	threshold = N/2 + 1
//
// where N is the number of currently-active authorities. So:
//
//	N=1 → threshold=1 (single-authority bootstrap can add a
//	                   second authority unilaterally; this is
//	                   the "trust the lone signer" posture
//	                   the chain already has by N=1's nature)
//	N=2 → threshold=2 (unanimity)
//	N=3 → threshold=2 (simple majority)
//	N=4 → threshold=3
//	N=5 → threshold=3
//
// A captured single authority is no worse than the pre-fork
// posture (they could already grief the chain). Past N=1, the
// strictly-greater-than-half rule keeps a captured minority
// from rotating themselves into majority.
//
// # Concurrency
//
// All methods on InMemoryAuthorityVoteStore are safe for
// concurrent use — same RWMutex pattern as InMemoryParamStore.
// Callers (the chain applier) hold the chain's serial apply
// lock when invoking RecordVote / Promote, so contention is
// effectively zero. Read methods (AllProposals, Lookup) are
// safe to call from monitoring / API goroutines.

import (
	"fmt"
	"sort"
	"sync"
)

// AuthorityVoteKey identifies a proposal tuple. Two votes with
// the same key target the same proposal; different keys are
// distinct proposals even if they mention the same address.
//
// Used as a map key, so all fields are value-typed and
// directly comparable.
type AuthorityVoteKey struct {
	Op              AuthorityOp
	Address         string
	EffectiveHeight uint64
}

// AuthorityVoteStore is the read+write surface the chain uses
// to tally authority-rotation votes and promote crossed
// proposals at activation time.
//
// As with ParamStore, persistence is the host's responsibility
// — the in-memory reference impl below carries no disk I/O.
type AuthorityVoteStore interface {
	// RecordVote applies a vote against the proposal named by
	// `key`. `currentAuthorities` is the live AuthorityList
	// at apply time, used to compute threshold and to
	// validate the voter membership at the call site (the
	// store does NOT enforce voter membership — that's the
	// applier's job, since the live AuthorityList changes
	// behind a different lock).
	//
	// Returns:
	//   proposal: the post-vote proposal state (Voters
	//             includes the new vote; Crossed reflects the
	//             post-vote tally vs threshold).
	//   crossed:  true ONLY when the current vote caused
	//             Crossed to flip from false → true. Lets the
	//             applier emit `authority-staged` exactly
	//             once per proposal (subsequent votes after
	//             crossing return crossed=false).
	//   err:      ErrDuplicateVote when `vote.Voter` already
	//             cast a vote against `key`.
	RecordVote(
		key AuthorityVoteKey,
		vote AuthorityVote,
		currentAuthorityCount int,
	) (proposal AuthorityProposal, crossed bool, err error)

	// AllProposals returns a deterministic snapshot of every
	// proposal currently held by the store, ordered by
	// EffectiveHeight ascending, then Op ascending, then
	// Address ascending. Used for snapshotting + the API.
	AllProposals() []AuthorityProposal

	// Lookup returns the proposal for `key`, or zero +
	// false if no such proposal exists. Used by the API and
	// by tests.
	Lookup(key AuthorityVoteKey) (AuthorityProposal, bool)

	// Promote walks every Crossed=true proposal whose
	// EffectiveHeight has been reached and returns them in
	// activation order (EffectiveHeight asc, Op asc, Address
	// asc). The store deletes them from its own state as
	// part of the call — promoted proposals are no longer
	// visible to AllProposals / Lookup after Promote returns.
	//
	// Idempotent: a second call at the same height is a
	// no-op.
	Promote(currentHeight uint64) []AuthorityProposal

	// DropVotesByAuthority removes every vote cast by
	// `authority` from every open (non-Crossed) proposal.
	// Called by the applier immediately after an
	// `authority-set / remove` proposal activates against
	// `authority` — votes by a now-revoked authority should
	// not count toward future threshold crossings.
	//
	// Returns the proposals whose vote-count changed (post-
	// drop view). If a drop pushes a proposal below 1 voter,
	// the proposal is deleted entirely and surfaced in the
	// returned slice with Voters=[] so the applier can emit
	// a `proposal-abandoned` event.
	DropVotesByAuthority(authority string) []AuthorityProposal

	// RecomputeCrossed walks every non-Crossed proposal and
	// marks it Crossed=true if its current vote tally meets
	// or exceeds the threshold derived from `authorityCount`.
	// Called by the applier after a remove activates and
	// shrinks the AuthorityList — a proposal that was short
	// by one vote may now satisfy the smaller threshold.
	//
	// Returns the proposals that newly crossed (so the
	// applier can emit `authority-staged`).
	RecomputeCrossed(authorityCount int, currentHeight uint64) []AuthorityProposal
}

// AuthorityThreshold returns the M-of-N threshold for an
// authority list of size n. See the package-level threshold
// rationale comment above. Exported so the CLI / API can
// surface the same number the chain uses.
//
// Returns 0 for n==0 (governance disabled — no proposal can
// ever cross). Returns 1 for n==1 (bootstrap), n/2+1 for n>=2.
func AuthorityThreshold(n int) int {
	if n <= 0 {
		return 0
	}
	if n == 1 {
		return 1
	}
	return n/2 + 1
}

// InMemoryAuthorityVoteStore is the reference implementation.
// Like InMemoryParamStore it persists nothing; the host wraps
// SaveSnapshot / LoadOrNew around it.
type InMemoryAuthorityVoteStore struct {
	mu        sync.RWMutex
	proposals map[AuthorityVoteKey]AuthorityProposal
}

// NewInMemoryAuthorityVoteStore constructs an empty store.
// Concurrency-safe; share with the applier and with monitoring
// readers.
func NewInMemoryAuthorityVoteStore() *InMemoryAuthorityVoteStore {
	return &InMemoryAuthorityVoteStore{
		proposals: make(map[AuthorityVoteKey]AuthorityProposal),
	}
}

// RecordVote implements AuthorityVoteStore.
func (s *InMemoryAuthorityVoteStore) RecordVote(
	key AuthorityVoteKey,
	vote AuthorityVote,
	currentAuthorityCount int,
) (AuthorityProposal, bool, error) {
	if vote.Voter == "" {
		return AuthorityProposal{}, false, fmt.Errorf(
			"%w: empty voter address", ErrPayloadInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	prop, ok := s.proposals[key]
	if !ok {
		prop = AuthorityProposal{
			Op:              key.Op,
			Address:         key.Address,
			EffectiveHeight: key.EffectiveHeight,
		}
	}
	for _, v := range prop.Voters {
		if v.Voter == vote.Voter {
			return prop, false, fmt.Errorf(
				"%w: voter=%q proposal=(%s,%s,%d)",
				ErrDuplicateVote, vote.Voter,
				key.Op, key.Address, key.EffectiveHeight)
		}
	}
	prop.Voters = append(prop.Voters, vote)
	sortVoters(prop.Voters)

	threshold := AuthorityThreshold(currentAuthorityCount)
	crossedNow := false
	if !prop.Crossed && threshold > 0 && len(prop.Voters) >= threshold {
		prop.Crossed = true
		prop.CrossedAtHeight = vote.SubmittedAtHeight
		crossedNow = true
	}
	s.proposals[key] = prop
	return prop, crossedNow, nil
}

// AllProposals implements AuthorityVoteStore. Deterministic
// order so two callers see the same list.
func (s *InMemoryAuthorityVoteStore) AllProposals() []AuthorityProposal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AuthorityProposal, 0, len(s.proposals))
	for _, p := range s.proposals {
		out = append(out, cloneProposal(p))
	}
	sortProposals(out)
	return out
}

// Lookup implements AuthorityVoteStore.
func (s *InMemoryAuthorityVoteStore) Lookup(
	key AuthorityVoteKey,
) (AuthorityProposal, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.proposals[key]
	if !ok {
		return AuthorityProposal{}, false
	}
	return cloneProposal(p), true
}

// Promote implements AuthorityVoteStore. Only Crossed
// proposals at-or-below currentHeight are returned and
// deleted; non-Crossed proposals at the same height are LEFT
// in place (the applier may decide to GC them via a separate
// reaper, but the conservative default is to keep votes
// visible until an operator explicitly tears down the
// proposal — relevant for post-mortem audits of a vote that
// just barely missed threshold).
func (s *InMemoryAuthorityVoteStore) Promote(
	currentHeight uint64,
) []AuthorityProposal {
	s.mu.Lock()
	defer s.mu.Unlock()

	var due []AuthorityProposal
	for key, prop := range s.proposals {
		if !prop.Crossed {
			continue
		}
		if prop.EffectiveHeight > currentHeight {
			continue
		}
		due = append(due, cloneProposal(prop))
		delete(s.proposals, key)
	}
	sortProposals(due)
	return due
}

// DropVotesByAuthority implements AuthorityVoteStore.
//
// Crossed proposals are NOT mutated by a vote-drop: once
// crossed, a proposal stages for activation; later authority
// changes shouldn't roll back a stable consensus decision.
// Only OPEN (non-Crossed) proposals have the named author's
// votes removed.
func (s *InMemoryAuthorityVoteStore) DropVotesByAuthority(
	authority string,
) []AuthorityProposal {
	if authority == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var changed []AuthorityProposal
	for key, prop := range s.proposals {
		if prop.Crossed {
			continue
		}
		filtered := prop.Voters[:0:0]
		filtered = append(filtered, prop.Voters...)
		out := filtered[:0]
		for _, v := range filtered {
			if v.Voter == authority {
				continue
			}
			out = append(out, v)
		}
		if len(out) == len(prop.Voters) {
			continue
		}
		if len(out) == 0 {
			delete(s.proposals, key)
			abandoned := cloneProposal(prop)
			abandoned.Voters = nil
			changed = append(changed, abandoned)
			continue
		}
		prop.Voters = out
		s.proposals[key] = prop
		changed = append(changed, cloneProposal(prop))
	}
	sortProposals(changed)
	return changed
}

// RecomputeCrossed implements AuthorityVoteStore.
func (s *InMemoryAuthorityVoteStore) RecomputeCrossed(
	authorityCount int,
	currentHeight uint64,
) []AuthorityProposal {
	threshold := AuthorityThreshold(authorityCount)
	if threshold == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var newlyCrossed []AuthorityProposal
	for key, prop := range s.proposals {
		if prop.Crossed {
			continue
		}
		if len(prop.Voters) < threshold {
			continue
		}
		prop.Crossed = true
		prop.CrossedAtHeight = currentHeight
		s.proposals[key] = prop
		newlyCrossed = append(newlyCrossed, cloneProposal(prop))
	}
	sortProposals(newlyCrossed)
	return newlyCrossed
}

// markCrossedForTesting flips Crossed=true (and CrossedAtHeight)
// on the proposal at `key`, without re-evaluating any
// threshold rule. Exported with the "ForTesting" suffix to
// mirror SetForTesting on the param store: the only callers
// SHOULD be the persistence loader (replaying a previously-
// crossed proposal whose threshold context might no longer
// hold) and unit tests. Production code must use RecordVote /
// RecomputeCrossed.
//
// Silently no-ops if the proposal does not exist.
func (s *InMemoryAuthorityVoteStore) markCrossedForTesting(
	key AuthorityVoteKey, crossedAtHeight uint64,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prop, ok := s.proposals[key]
	if !ok {
		return
	}
	prop.Crossed = true
	prop.CrossedAtHeight = crossedAtHeight
	s.proposals[key] = prop
}

// cloneProposal returns a deep copy with an independent Voters
// slice so callers cannot mutate the store's internal state by
// holding onto a returned proposal.
func cloneProposal(p AuthorityProposal) AuthorityProposal {
	out := p
	if len(p.Voters) > 0 {
		out.Voters = make([]AuthorityVote, len(p.Voters))
		copy(out.Voters, p.Voters)
	}
	return out
}

// sortVoters orders by SubmittedAtHeight asc, then Voter asc.
// Mutates the slice in place.
func sortVoters(vs []AuthorityVote) {
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].SubmittedAtHeight != vs[j].SubmittedAtHeight {
			return vs[i].SubmittedAtHeight < vs[j].SubmittedAtHeight
		}
		return vs[i].Voter < vs[j].Voter
	})
}

// sortProposals orders by EffectiveHeight asc, then Op asc,
// then Address asc. Used for every deterministic snapshot
// surface (AllProposals, Promote return, DropVotesByAuthority,
// RecomputeCrossed). Mutates in place.
func sortProposals(ps []AuthorityProposal) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].EffectiveHeight != ps[j].EffectiveHeight {
			return ps[i].EffectiveHeight < ps[j].EffectiveHeight
		}
		if ps[i].Op != ps[j].Op {
			return ps[i].Op < ps[j].Op
		}
		return ps[i].Address < ps[j].Address
	})
}

// Compile-time assertion.
var _ AuthorityVoteStore = (*InMemoryAuthorityVoteStore)(nil)
