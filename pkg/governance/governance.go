package governance

import (
    "errors"
    "sync"
)

// Proposal represents a governance proposal.
type Proposal struct {
    ID          string
    Description string
    VotesFor    int
    VotesAgainst int
    Finalized   bool
}

// SnapshotVoting manages token-weighted voting for governance proposals.
type SnapshotVoting struct {
    Mu        sync.RWMutex
    Proposals map[string]*Proposal
    Voters    map[string]int // voter ID to token weight
}

// NewSnapshotVoting creates a new SnapshotVoting instance.
func NewSnapshotVoting() *SnapshotVoting {
    return &SnapshotVoting{
        Proposals: make(map[string]*Proposal),
        Voters:    make(map[string]int),
    }
}

// AddProposal adds a new governance proposal.
func (sv *SnapshotVoting) AddProposal(id, description string) error {
    sv.Mu.Lock()
    defer sv.Mu.Unlock()
    if _, exists := sv.Proposals[id]; exists {
        return errors.New("proposal already exists")
    }
    sv.Proposals[id] = &Proposal{
        ID:          id,
        Description: description,
    }
    governance.LogProposalAdded(id)
    return nil
}

// Vote casts a vote for or against a proposal by a voter with token weight.
func (sv *SnapshotVoting) Vote(proposalID, voterID string, weight int, support bool) error {
    sv.Mu.Lock()
    defer sv.Mu.Unlock()
    proposal, exists := sv.Proposals[proposalID]
    if !exists {
        return errors.New("proposal not found")
    }
    if proposal.Finalized {
        return errors.New("proposal already finalized")
    }
    voterWeight, ok := sv.Voters[voterID]
    if !ok || voterWeight < weight {
        return errors.New("insufficient voting weight")
    }
    if support {
        proposal.VotesFor += weight
    } else {
        proposal.VotesAgainst += weight
    }
    sv.Voters[voterID] -= weight
    governance.LogVoteCast(proposalID, voterID, weight, support)
    return nil
}

// FinalizeProposal finalizes a proposal based on votes.
func (sv *SnapshotVoting) FinalizeProposal(proposalID string) (bool, error) {
    sv.Mu.Lock()
    defer sv.Mu.Unlock()
    proposal, exists := sv.Proposals[proposalID]
    if !exists {
        return false, errors.New("proposal not found")
    }
    if proposal.Finalized {
        return false, errors.New("proposal already finalized")
    }
    proposal.Finalized = true
    passed := proposal.VotesFor > proposal.VotesAgainst
    governance.LogProposalFinalized(proposalID, passed)
    return passed, nil
}
