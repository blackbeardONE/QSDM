package governance

import (
    "sync"
    "errors"
    "time"
    "fmt"
)

// VoteOption represents a voting option.
type VoteOption int

const (
    VoteYes VoteOption = iota
    VoteNo
    VoteAbstain
)

// Snapshot represents a governance snapshot for voting.
type Snapshot struct {
    ID          string
    CreatedAt   time.Time
    Expiry      time.Time
    Votes       map[string]VoteOption // voterID -> vote
    TokenWeight map[string]float64    // voterID -> token weight
    mu          sync.RWMutex
}

// NewSnapshot creates a new governance snapshot with expiry duration.
func NewSnapshot(id string, duration time.Duration) *Snapshot {
    return &Snapshot{
        ID:          id,
        CreatedAt:   time.Now(),
        Expiry:      time.Now().Add(duration),
        Votes:       make(map[string]VoteOption),
        TokenWeight: make(map[string]float64),
    }
}

// CastVote casts a vote for a voter.
func (s *Snapshot) CastVote(voterID string, option VoteOption) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if time.Now().After(s.Expiry) {
        return errors.New("voting period expired")
    }
    s.Votes[voterID] = option
    return nil
}

// SetTokenWeight sets the token weight for a voter.
func (s *Snapshot) SetTokenWeight(voterID string, weight float64) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.TokenWeight[voterID] = weight
}

// TallyVotes tallies the votes weighted by token holdings.
func (s *Snapshot) TallyVotes() (yes, no, abstain float64) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    for voterID, vote := range s.Votes {
        weight := s.TokenWeight[voterID]
        switch vote {
        case VoteYes:
            yes += weight
        case VoteNo:
            no += weight
        case VoteAbstain:
            abstain += weight
        }
    }
    return
}

// Result returns the voting result as a string.
func (s *Snapshot) Result() string {
    yes, no, abstain := s.TallyVotes()
    total := yes + no + abstain
    if total == 0 {
        return "No votes cast"
    }
    return fmt.Sprintf("Yes: %.2f%%, No: %.2f%%, Abstain: %.2f%%",
        (yes/total)*100, (no/total)*100, (abstain/total)*100)
}
