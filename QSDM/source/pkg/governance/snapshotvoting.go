// Additional imports for persistence, time, and quorum
package governance

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// Proposal represents a governance proposal.
type Proposal struct {
	ID           string
	Description  string
	VotesFor     int
	VotesAgainst int
	Finalized    bool
	CreatedAt    time.Time
	ExpiresAt    time.Time
	Quorum       int
}

// SnapshotVoting manages proposals and voting with persistence and quorum.
type SnapshotVoting struct {
	Proposals map[string]*Proposal
	Mu        sync.RWMutex
	filePath  string
}

// NewSnapshotVoting creates a new SnapshotVoting instance with persistence file.
func NewSnapshotVoting(filePath string) *SnapshotVoting {
	sv := &SnapshotVoting{
		Proposals: make(map[string]*Proposal),
		filePath:  filePath,
	}
	sv.loadFromFile()
	return sv
}

// loadFromFile loads proposals from the persistence file.
func (sv *SnapshotVoting) loadFromFile() error {
	sv.Mu.Lock()
	defer sv.Mu.Unlock()
	file, err := os.Open(sv.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No file yet
		}
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	return decoder.Decode(&sv.Proposals)
}

// saveToFile saves proposals to the persistence file.
// NOTE: This function assumes the caller already holds the lock.
func (sv *SnapshotVoting) saveToFile() error {
	file, err := os.Create(sv.filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	return encoder.Encode(sv.Proposals)
}

// AddProposal adds a new proposal with expiration and quorum.
func (sv *SnapshotVoting) AddProposal(id, description string, duration time.Duration, quorum int) error {
	sv.Mu.Lock()
	defer sv.Mu.Unlock()
	if _, exists := sv.Proposals[id]; exists {
		return errors.New("proposal already exists")
	}
	now := time.Now()
	sv.Proposals[id] = &Proposal{
		ID:          id,
		Description: description,
		CreatedAt:   now,
		ExpiresAt:   now.Add(duration),
		Quorum:      quorum,
	}
	return sv.saveToFile()
}

// Vote casts a vote on a proposal.
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
	if time.Now().After(proposal.ExpiresAt) {
		return errors.New("proposal expired")
	}
	if support {
		proposal.VotesFor += weight
	} else {
		proposal.VotesAgainst += weight
	}
	return sv.saveToFile()
}

// FinalizeProposal finalizes a proposal and returns whether it passed.
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
	if time.Now().Before(proposal.ExpiresAt) {
		return false, errors.New("proposal not yet expired")
	}
	totalVotes := proposal.VotesFor + proposal.VotesAgainst
	if totalVotes < proposal.Quorum {
		proposal.Finalized = true
		return false, errors.New("quorum not reached")
	}
	proposal.Finalized = true
	err := sv.saveToFile()
	if err != nil {
		return false, err
	}
	return proposal.VotesFor > proposal.VotesAgainst, nil
}
