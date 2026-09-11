package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

const (
	// ConsensusMembershipTransitionSchemaVersion identifies this deterministic
	// state format. A format change requires a new version.
	ConsensusMembershipTransitionSchemaVersion uint32 = 1

	maxPendingConsensusMembershipTransitions   = 16
	maxFinalizedConsensusMembershipTransitions = 64

	membershipTransitionStateDomain    = "qsdm/consensus-membership-state/v1"
	membershipTransitionProposalDomain = "qsdm/consensus-membership-transition-proposal/v1"
	membershipTransitionVoteDomain     = "qsdm/consensus-membership-transition-vote/v1"
)

var (
	ErrConsensusMembershipTransitionUnknown        = errors.New("chain: consensus membership transition is unknown")
	ErrConsensusMembershipTransitionDuplicate      = errors.New("chain: consensus membership transition is duplicate")
	ErrConsensusMembershipTransitionConflict       = errors.New("chain: consensus membership transition conflicts")
	ErrConsensusMembershipTransitionStale          = errors.New("chain: consensus membership transition is stale")
	ErrConsensusMembershipTransitionUnauthorized   = errors.New("chain: consensus membership transition is unauthorized")
	ErrConsensusMembershipTransitionQuorum         = errors.New("chain: consensus membership transition already has quorum")
	ErrConsensusMembershipTransitionAdvanceSkipped = errors.New("chain: consensus membership transition activation was skipped")
)

// ConsensusMembershipTransitionVote is an old-set approval for one exact
// membership transition proposal.
type ConsensusMembershipTransitionVote struct {
	ProposalID string      `json:"proposal_id"`
	Signer     string      `json:"signer"`
	Auth       BFTWireAuth `json:"auth"`
}

// ConsensusMembershipTransitionProposal names a complete replacement
// membership and the exact height at which it must take effect.
type ConsensusMembershipTransitionProposal struct {
	SchemaVersion               uint32              `json:"schema_version"`
	ID                          string              `json:"id"`
	NetworkID                   string              `json:"network_id"`
	ActiveMembershipFingerprint string              `json:"active_membership_fingerprint"`
	NextMembership              ConsensusMembership `json:"next_membership"`
	ActivationHeight            uint64              `json:"activation_height"`
	Proposer                    string              `json:"proposer"`
	Auth                        BFTWireAuth         `json:"auth"`
}

type ConsensusMembershipTransitionOutcome string

const (
	ConsensusMembershipTransitionActivated  ConsensusMembershipTransitionOutcome = "activated"
	ConsensusMembershipTransitionExpired    ConsensusMembershipTransitionOutcome = "expired"
	ConsensusMembershipTransitionSuperseded ConsensusMembershipTransitionOutcome = "superseded"
)

// ConsensusMembershipTransitionFinalization retains bounded replay metadata.
type ConsensusMembershipTransitionFinalization struct {
	Proposal            ConsensusMembershipTransitionProposal `json:"proposal"`
	Outcome             ConsensusMembershipTransitionOutcome  `json:"outcome"`
	FinalizedHeight     uint64                                `json:"finalized_height"`
	ApprovedVotingPower uint64                                `json:"approved_voting_power"`
	RequiredVotingPower uint64                                `json:"required_voting_power"`
}

// ConsensusMembershipTransitionStatus is a defensive pending-state view.
type ConsensusMembershipTransitionStatus struct {
	Proposal            ConsensusMembershipTransitionProposal `json:"proposal"`
	Votes               []ConsensusMembershipTransitionVote   `json:"votes"`
	ApprovedVotingPower uint64                                `json:"approved_voting_power"`
	RequiredVotingPower uint64                                `json:"required_voting_power"`
}

type consensusMembershipTransition struct {
	proposal ConsensusMembershipTransitionProposal
	votes    map[string]ConsensusMembershipTransitionVote
}

// ConsensusMembershipState is an inactive foundation for signed, old-set
// quorum membership transitions. It is intentionally not attached to the
// current block state root, transaction dispatcher, or live validator path.
type ConsensusMembershipState struct {
	mu        sync.RWMutex
	active    ConsensusMembership
	pending   map[string]consensusMembershipTransition
	finalized []ConsensusMembershipTransitionFinalization
}

func NewConsensusMembershipState(active ConsensusMembership) (*ConsensusMembershipState, error) {
	if err := active.Validate(); err != nil {
		return nil, fmt.Errorf("chain: initial consensus membership: %w", err)
	}
	return &ConsensusMembershipState{
		active: cloneConsensusMembership(active), pending: make(map[string]consensusMembershipTransition),
	}, nil
}

func (s *ConsensusMembershipState) ActiveMembership() ConsensusMembership {
	if s == nil {
		return ConsensusMembership{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConsensusMembership(s.active)
}

func (s *ConsensusMembershipState) Pending() []ConsensusMembershipTransitionStatus {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ConsensusMembershipTransitionStatus, 0, len(s.pending))
	for _, transition := range s.pending {
		approved, required, _ := transition.power(s.active)
		out = append(out, ConsensusMembershipTransitionStatus{
			Proposal: cloneConsensusMembershipTransitionProposal(transition.proposal),
			Votes:    transition.votesSorted(), ApprovedVotingPower: approved, RequiredVotingPower: required,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Proposal.ActivationHeight == out[j].Proposal.ActivationHeight {
			return out[i].Proposal.ID < out[j].Proposal.ID
		}
		return out[i].Proposal.ActivationHeight < out[j].Proposal.ActivationHeight
	})
	return out
}

func (s *ConsensusMembershipState) Finalized() []ConsensusMembershipTransitionFinalization {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConsensusMembershipTransitionFinalizations(s.finalized)
}

// NewConsensusMembershipTransitionProposal builds a proposal whose activation
// height is the replacement membership's EffectiveHeight.
func NewConsensusMembershipTransitionProposal(active, next ConsensusMembership, signer BFTSigner) (ConsensusMembershipTransitionProposal, error) {
	if err := active.Validate(); err != nil {
		return ConsensusMembershipTransitionProposal{}, err
	}
	if err := next.Validate(); err != nil {
		return ConsensusMembershipTransitionProposal{}, err
	}
	if next.NetworkID != active.NetworkID {
		return ConsensusMembershipTransitionProposal{}, fmt.Errorf("%w: network ID differs", ErrConsensusMembershipTransitionConflict)
	}
	if next.EffectiveHeight <= active.EffectiveHeight {
		return ConsensusMembershipTransitionProposal{}, fmt.Errorf("%w: activation height is not after active membership", ErrConsensusMembershipTransitionStale)
	}
	if signer == nil {
		return ConsensusMembershipTransitionProposal{}, fmt.Errorf("%w: nil proposer", ErrConsensusMembershipTransitionUnauthorized)
	}
	activeRoot, _ := active.Fingerprint()
	nextRoot, _ := next.Fingerprint()
	proposal := ConsensusMembershipTransitionProposal{
		SchemaVersion: ConsensusMembershipTransitionSchemaVersion, NetworkID: active.NetworkID,
		ActiveMembershipFingerprint: activeRoot, NextMembership: cloneConsensusMembership(next),
		ActivationHeight: next.EffectiveHeight, Proposer: BFTValidatorAddress(signer.GetPublicKey()),
	}
	proposal.ID = membershipTransitionProposalID(proposal.NetworkID, activeRoot, nextRoot, proposal.ActivationHeight)
	auth, err := signAuth(signer, membershipTransitionProposalDigest(proposal))
	if err != nil {
		return ConsensusMembershipTransitionProposal{}, err
	}
	proposal.Auth = auth
	return proposal, nil
}

func NewConsensusMembershipTransitionVote(proposal ConsensusMembershipTransitionProposal, signer BFTSigner) (ConsensusMembershipTransitionVote, error) {
	if signer == nil {
		return ConsensusMembershipTransitionVote{}, fmt.Errorf("%w: nil signer", ErrConsensusMembershipTransitionUnauthorized)
	}
	vote := ConsensusMembershipTransitionVote{ProposalID: proposal.ID, Signer: BFTValidatorAddress(signer.GetPublicKey())}
	auth, err := signAuth(signer, membershipTransitionVoteDigest(vote.ProposalID, vote.Signer))
	if err != nil {
		return ConsensusMembershipTransitionVote{}, err
	}
	vote.Auth = auth
	return vote, nil
}

func (s *ConsensusMembershipState) RecordProposal(proposal ConsensusMembershipTransitionProposal, currentHeight uint64) error {
	if s == nil {
		return errors.New("chain: nil consensus membership state")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateMembershipTransitionProposal(proposal, s.active); err != nil {
		return err
	}
	if currentHeight >= proposal.ActivationHeight {
		return fmt.Errorf("%w: activation=%d current=%d", ErrConsensusMembershipTransitionStale, proposal.ActivationHeight, currentHeight)
	}
	if _, exists := s.pending[proposal.ID]; exists {
		return fmt.Errorf("%w: proposal %s is pending", ErrConsensusMembershipTransitionDuplicate, proposal.ID)
	}
	for _, final := range s.finalized {
		if final.Proposal.ID == proposal.ID {
			return fmt.Errorf("%w: proposal %s was finalized", ErrConsensusMembershipTransitionDuplicate, proposal.ID)
		}
	}
	for _, pending := range s.pending {
		if pending.proposal.ActivationHeight == proposal.ActivationHeight {
			return fmt.Errorf("%w: activation height %d is occupied", ErrConsensusMembershipTransitionConflict, proposal.ActivationHeight)
		}
	}
	if len(s.pending) >= maxPendingConsensusMembershipTransitions {
		return fmt.Errorf("%w: pending queue is full", ErrConsensusMembershipTransitionConflict)
	}
	s.pending[proposal.ID] = consensusMembershipTransition{
		proposal: cloneConsensusMembershipTransitionProposal(proposal),
		votes:    make(map[string]ConsensusMembershipTransitionVote),
	}
	return nil
}

func (s *ConsensusMembershipState) RecordVote(proposalID string, vote ConsensusMembershipTransitionVote, currentHeight uint64) error {
	if s == nil {
		return errors.New("chain: nil consensus membership state")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	transition, exists := s.pending[proposalID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrConsensusMembershipTransitionUnknown, proposalID)
	}
	if currentHeight >= transition.proposal.ActivationHeight {
		return fmt.Errorf("%w: activation=%d current=%d", ErrConsensusMembershipTransitionStale, transition.proposal.ActivationHeight, currentHeight)
	}
	if vote.ProposalID != proposalID {
		return fmt.Errorf("%w: vote names another proposal", ErrConsensusMembershipTransitionConflict)
	}
	if _, exists := transition.votes[vote.Signer]; exists {
		return fmt.Errorf("%w: signer %s", ErrConsensusMembershipTransitionDuplicate, vote.Signer)
	}
	approved, required, err := transition.power(s.active)
	if err != nil {
		return err
	}
	if approved >= required {
		return fmt.Errorf("%w: %d/%d", ErrConsensusMembershipTransitionQuorum, approved, required)
	}
	if err := validateMembershipTransitionVote(vote, proposalID, s.active); err != nil {
		return err
	}
	transition.votes[vote.Signer] = cloneConsensusMembershipTransitionVote(vote)
	s.pending[proposalID] = transition
	return nil
}

// FinalizeAtHeight resolves proposals exactly at currentHeight. Advancing past
// a pending activation height is an error rather than a late membership switch.
func (s *ConsensusMembershipState) FinalizeAtHeight(currentHeight uint64) error {
	if s == nil {
		return errors.New("chain: nil consensus membership state")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, transition := range s.pending {
		if transition.proposal.ActivationHeight < currentHeight {
			return fmt.Errorf("%w: proposal %s activation=%d current=%d", ErrConsensusMembershipTransitionAdvanceSkipped, transition.proposal.ID, transition.proposal.ActivationHeight, currentHeight)
		}
	}
	var due []string
	for id, transition := range s.pending {
		if transition.proposal.ActivationHeight == currentHeight {
			due = append(due, id)
		}
	}
	sort.Strings(due)
	for _, id := range due {
		transition := s.pending[id]
		approved, required, err := transition.power(s.active)
		if err != nil {
			return err
		}
		delete(s.pending, id)
		if approved < required {
			s.appendFinalized(transition.proposal, ConsensusMembershipTransitionExpired, currentHeight, approved, required)
			continue
		}
		oldActive := cloneConsensusMembership(s.active)
		s.active = cloneConsensusMembership(transition.proposal.NextMembership)
		s.appendFinalized(transition.proposal, ConsensusMembershipTransitionActivated, currentHeight, approved, required)
		s.supersedePending(currentHeight, oldActive)
	}
	return nil
}

// Clone and RestoreFrom are typed replay helpers. They are intentionally not
// ChainReplayApplier until transition transactions and state-root integration
// are designed and activated together.
func (s *ConsensusMembershipState) Clone() *ConsensusMembershipState {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &ConsensusMembershipState{
		active: cloneConsensusMembership(s.active), pending: cloneConsensusMembershipTransitions(s.pending),
		finalized: cloneConsensusMembershipTransitionFinalizations(s.finalized),
	}
}

func (s *ConsensusMembershipState) RestoreFrom(snapshot *ConsensusMembershipState) error {
	if s == nil || snapshot == nil {
		return errors.New("chain: nil consensus membership state")
	}
	copy := snapshot.Clone()
	if err := copy.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active, s.pending, s.finalized = copy.active, copy.pending, copy.finalized
	return nil
}

func (s *ConsensusMembershipState) CanonicalBytes() ([]byte, error) {
	if s == nil {
		return nil, errors.New("chain: nil consensus membership state")
	}
	copy := s.Clone()
	if err := copy.validate(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(membershipTransitionStateDomain)
	out.WriteByte(0)
	writeMembershipUint32(&out, ConsensusMembershipTransitionSchemaVersion)
	active, _ := copy.active.CanonicalBytes()
	writeMembershipBytes(&out, active)

	pending := make([]consensusMembershipTransition, 0, len(copy.pending))
	for _, transition := range copy.pending {
		pending = append(pending, transition)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].proposal.ID < pending[j].proposal.ID })
	writeMembershipUint32(&out, uint32(len(pending)))
	for _, transition := range pending {
		writeMembershipTransitionProposal(&out, transition.proposal)
		votes := transition.votesSorted()
		writeMembershipUint32(&out, uint32(len(votes)))
		for _, vote := range votes {
			writeMembershipTransitionVote(&out, vote)
		}
	}

	finalized := cloneConsensusMembershipTransitionFinalizations(copy.finalized)
	sort.Slice(finalized, func(i, j int) bool {
		if finalized[i].FinalizedHeight == finalized[j].FinalizedHeight {
			return finalized[i].Proposal.ID < finalized[j].Proposal.ID
		}
		return finalized[i].FinalizedHeight < finalized[j].FinalizedHeight
	})
	writeMembershipUint32(&out, uint32(len(finalized)))
	for _, final := range finalized {
		writeMembershipTransitionProposal(&out, final.Proposal)
		writeMembershipString(&out, string(final.Outcome))
		writeMembershipUint64(&out, final.FinalizedHeight)
		writeMembershipUint64(&out, final.ApprovedVotingPower)
		writeMembershipUint64(&out, final.RequiredVotingPower)
	}
	return out.Bytes(), nil
}

func (s *ConsensusMembershipState) StateRoot() (string, error) {
	raw, err := s.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s *ConsensusMembershipState) validate() error {
	if err := s.active.Validate(); err != nil {
		return err
	}
	if len(s.pending) > maxPendingConsensusMembershipTransitions || len(s.finalized) > maxFinalizedConsensusMembershipTransitions {
		return errors.New("chain: membership transition state exceeds its bounded storage")
	}
	heights := map[uint64]struct{}{}
	for id, transition := range s.pending {
		if id != transition.proposal.ID {
			return errors.New("chain: membership transition map key mismatch")
		}
		if err := validateMembershipTransitionProposal(transition.proposal, s.active); err != nil {
			return err
		}
		if _, exists := heights[transition.proposal.ActivationHeight]; exists {
			return fmt.Errorf("%w: duplicate activation height", ErrConsensusMembershipTransitionConflict)
		}
		heights[transition.proposal.ActivationHeight] = struct{}{}
		for signer, vote := range transition.votes {
			if signer != vote.Signer {
				return errors.New("chain: membership transition vote map key mismatch")
			}
			if err := validateMembershipTransitionVote(vote, id, s.active); err != nil {
				return err
			}
		}
	}
	for _, final := range s.finalized {
		if err := validateMembershipTransitionProposalShape(final.Proposal); err != nil {
			return err
		}
		if final.RequiredVotingPower == 0 {
			return errors.New("chain: finalized membership transition has no quorum")
		}
		switch final.Outcome {
		case ConsensusMembershipTransitionActivated, ConsensusMembershipTransitionExpired:
			if final.FinalizedHeight != final.Proposal.ActivationHeight {
				return errors.New("chain: finalization did not occur at the activation height")
			}
		case ConsensusMembershipTransitionSuperseded:
			if final.FinalizedHeight > final.Proposal.ActivationHeight {
				return errors.New("chain: superseded transition was finalized too late")
			}
		default:
			return fmt.Errorf("chain: unknown membership transition outcome %q", final.Outcome)
		}
	}
	return nil
}

func (s *ConsensusMembershipState) appendFinalized(proposal ConsensusMembershipTransitionProposal, outcome ConsensusMembershipTransitionOutcome, height, approved, required uint64) {
	s.finalized = append(s.finalized, ConsensusMembershipTransitionFinalization{
		Proposal: cloneConsensusMembershipTransitionProposal(proposal), Outcome: outcome,
		FinalizedHeight: height, ApprovedVotingPower: approved, RequiredVotingPower: required,
	})
	if extra := len(s.finalized) - maxFinalizedConsensusMembershipTransitions; extra > 0 {
		s.finalized = append([]ConsensusMembershipTransitionFinalization(nil), s.finalized[extra:]...)
	}
}

func (s *ConsensusMembershipState) supersedePending(height uint64, oldActive ConsensusMembership) {
	ids := make([]string, 0, len(s.pending))
	for id := range s.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		transition := s.pending[id]
		approved, required, err := transition.power(oldActive)
		if err != nil {
			approved, required = 0, 1
		}
		delete(s.pending, id)
		s.appendFinalized(transition.proposal, ConsensusMembershipTransitionSuperseded, height, approved, required)
	}
}

func (transition consensusMembershipTransition) votesSorted() []ConsensusMembershipTransitionVote {
	out := make([]ConsensusMembershipTransitionVote, 0, len(transition.votes))
	for _, vote := range transition.votes {
		out = append(out, cloneConsensusMembershipTransitionVote(vote))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Signer < out[j].Signer })
	return out
}

func (transition consensusMembershipTransition) power(active ConsensusMembership) (uint64, uint64, error) {
	required, err := active.RequiredQuorumVotingPower()
	if err != nil {
		return 0, 0, err
	}
	var total uint64
	for signer := range transition.votes {
		member, exists := membershipMember(active, signer)
		if !exists {
			return 0, 0, fmt.Errorf("%w: signer %s", ErrConsensusMembershipTransitionUnauthorized, signer)
		}
		if total > ^uint64(0)-member.VotingPower {
			return 0, 0, errors.New("chain: membership transition approval overflow")
		}
		total += member.VotingPower
	}
	return total, required, nil
}

func validateMembershipTransitionProposal(proposal ConsensusMembershipTransitionProposal, active ConsensusMembership) error {
	if err := validateMembershipTransitionProposalShape(proposal); err != nil {
		return err
	}
	activeRoot, _ := active.Fingerprint()
	if proposal.NetworkID != active.NetworkID || proposal.ActiveMembershipFingerprint != activeRoot {
		return fmt.Errorf("%w: proposal does not bind the active membership", ErrConsensusMembershipTransitionStale)
	}
	if proposal.ActivationHeight <= active.EffectiveHeight {
		return fmt.Errorf("%w: activation is not after active membership", ErrConsensusMembershipTransitionStale)
	}
	member, exists := membershipMember(active, proposal.Proposer)
	if !exists {
		return fmt.Errorf("%w: proposer %s", ErrConsensusMembershipTransitionUnauthorized, proposal.Proposer)
	}
	key, _ := decodeCanonicalMembershipPublicKey(member.ConsensusPublicKeyHex)
	if !bytes.Equal(key, proposal.Auth.PublicKey) {
		return fmt.Errorf("%w: proposer key mismatch", ErrConsensusMembershipTransitionUnauthorized)
	}
	if err := verifyAuth(proposal.Auth, membershipTransitionProposalDigest(proposal), proposal.Proposer); err != nil {
		return fmt.Errorf("%w: proposal signature: %v", ErrConsensusMembershipTransitionUnauthorized, err)
	}
	return nil
}

func validateMembershipTransitionProposalShape(proposal ConsensusMembershipTransitionProposal) error {
	if proposal.SchemaVersion != ConsensusMembershipTransitionSchemaVersion {
		return fmt.Errorf("%w: unsupported transition schema", ErrConsensusMembershipTransitionConflict)
	}
	if proposal.NetworkID == "" || proposal.NetworkID != proposal.NextMembership.NetworkID {
		return fmt.Errorf("%w: network mismatch", ErrConsensusMembershipTransitionConflict)
	}
	if err := proposal.NextMembership.Validate(); err != nil {
		return fmt.Errorf("%w: replacement membership: %v", ErrConsensusMembershipTransitionConflict, err)
	}
	if proposal.ActivationHeight != proposal.NextMembership.EffectiveHeight {
		return fmt.Errorf("%w: effective height mismatch", ErrConsensusMembershipTransitionConflict)
	}
	nextRoot, _ := proposal.NextMembership.Fingerprint()
	if proposal.ID != membershipTransitionProposalID(proposal.NetworkID, proposal.ActiveMembershipFingerprint, nextRoot, proposal.ActivationHeight) {
		return fmt.Errorf("%w: noncanonical proposal ID", ErrConsensusMembershipTransitionConflict)
	}
	return nil
}

func validateMembershipTransitionVote(vote ConsensusMembershipTransitionVote, proposalID string, active ConsensusMembership) error {
	if vote.ProposalID != proposalID {
		return fmt.Errorf("%w: proposal mismatch", ErrConsensusMembershipTransitionConflict)
	}
	member, exists := membershipMember(active, vote.Signer)
	if !exists {
		return fmt.Errorf("%w: vote signer %s", ErrConsensusMembershipTransitionUnauthorized, vote.Signer)
	}
	key, _ := decodeCanonicalMembershipPublicKey(member.ConsensusPublicKeyHex)
	if !bytes.Equal(key, vote.Auth.PublicKey) {
		return fmt.Errorf("%w: vote key mismatch", ErrConsensusMembershipTransitionUnauthorized)
	}
	if err := verifyAuth(vote.Auth, membershipTransitionVoteDigest(vote.ProposalID, vote.Signer), vote.Signer); err != nil {
		return fmt.Errorf("%w: vote signature: %v", ErrConsensusMembershipTransitionUnauthorized, err)
	}
	return nil
}

func membershipMember(membership ConsensusMembership, address string) (ConsensusMember, bool) {
	for _, member := range membership.Members {
		if member.Address == address {
			return member, true
		}
	}
	return ConsensusMember{}, false
}

func membershipTransitionProposalID(networkID, activeRoot, nextRoot string, height uint64) string {
	var out bytes.Buffer
	out.WriteString(membershipTransitionProposalDomain)
	out.WriteByte(0)
	writeMembershipString(&out, networkID)
	writeMembershipString(&out, activeRoot)
	writeMembershipString(&out, nextRoot)
	writeMembershipUint64(&out, height)
	sum := sha256.Sum256(out.Bytes())
	return hex.EncodeToString(sum[:])
}

func membershipTransitionProposalDigest(proposal ConsensusMembershipTransitionProposal) []byte {
	nextRoot, _ := proposal.NextMembership.Fingerprint()
	var out bytes.Buffer
	out.WriteString(membershipTransitionProposalDomain)
	out.WriteByte(0)
	writeMembershipUint32(&out, proposal.SchemaVersion)
	writeMembershipString(&out, proposal.ID)
	writeMembershipString(&out, proposal.NetworkID)
	writeMembershipString(&out, proposal.ActiveMembershipFingerprint)
	writeMembershipString(&out, nextRoot)
	writeMembershipUint64(&out, proposal.ActivationHeight)
	writeMembershipString(&out, proposal.Proposer)
	sum := sha256.Sum256(out.Bytes())
	return sum[:]
}

func membershipTransitionVoteDigest(proposalID, signer string) []byte {
	var out bytes.Buffer
	out.WriteString(membershipTransitionVoteDomain)
	out.WriteByte(0)
	writeMembershipString(&out, proposalID)
	writeMembershipString(&out, signer)
	sum := sha256.Sum256(out.Bytes())
	return sum[:]
}

func writeMembershipTransitionProposal(out *bytes.Buffer, proposal ConsensusMembershipTransitionProposal) {
	next, _ := proposal.NextMembership.CanonicalBytes()
	writeMembershipUint32(out, proposal.SchemaVersion)
	writeMembershipString(out, proposal.ID)
	writeMembershipString(out, proposal.NetworkID)
	writeMembershipString(out, proposal.ActiveMembershipFingerprint)
	writeMembershipBytes(out, next)
	writeMembershipUint64(out, proposal.ActivationHeight)
	writeMembershipString(out, proposal.Proposer)
	writeMembershipBytes(out, proposal.Auth.PublicKey)
	writeMembershipBytes(out, proposal.Auth.Signature)
}

func writeMembershipTransitionVote(out *bytes.Buffer, vote ConsensusMembershipTransitionVote) {
	writeMembershipString(out, vote.ProposalID)
	writeMembershipString(out, vote.Signer)
	writeMembershipBytes(out, vote.Auth.PublicKey)
	writeMembershipBytes(out, vote.Auth.Signature)
}

func cloneConsensusMembershipTransitionProposal(value ConsensusMembershipTransitionProposal) ConsensusMembershipTransitionProposal {
	copy := value
	copy.NextMembership = cloneConsensusMembership(value.NextMembership)
	copy.Auth = BFTWireAuth{PublicKey: append([]byte(nil), value.Auth.PublicKey...), Signature: append([]byte(nil), value.Auth.Signature...)}
	return copy
}

func cloneConsensusMembershipTransitionVote(value ConsensusMembershipTransitionVote) ConsensusMembershipTransitionVote {
	copy := value
	copy.Auth = BFTWireAuth{PublicKey: append([]byte(nil), value.Auth.PublicKey...), Signature: append([]byte(nil), value.Auth.Signature...)}
	return copy
}

func cloneConsensusMembershipTransitions(source map[string]consensusMembershipTransition) map[string]consensusMembershipTransition {
	out := make(map[string]consensusMembershipTransition, len(source))
	for id, transition := range source {
		votes := make(map[string]ConsensusMembershipTransitionVote, len(transition.votes))
		for signer, vote := range transition.votes {
			votes[signer] = cloneConsensusMembershipTransitionVote(vote)
		}
		out[id] = consensusMembershipTransition{proposal: cloneConsensusMembershipTransitionProposal(transition.proposal), votes: votes}
	}
	return out
}

func cloneConsensusMembershipTransitionFinalizations(source []ConsensusMembershipTransitionFinalization) []ConsensusMembershipTransitionFinalization {
	out := make([]ConsensusMembershipTransitionFinalization, len(source))
	for i, final := range source {
		out[i] = final
		out[i].Proposal = cloneConsensusMembershipTransitionProposal(final.Proposal)
	}
	return out
}
