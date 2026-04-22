package chain

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrBFTEquivocation is returned when the same proposer issues a second Propose for the same height and round with a different block_hash (double-sign).
var ErrBFTEquivocation = errors.New("chain: BFT proposer equivocation (conflicting block_hash at same height and round)")

// ProposerEquivocationError carries structured context for evidence / diagnostics (unwraps to ErrBFTEquivocation).
type ProposerEquivocationError struct {
	Height       uint64
	Round        uint32
	Proposer     string
	ExistingHash string
	NewHash      string
}

func (e *ProposerEquivocationError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%v at height %d round %d (existing %q new %q)", ErrBFTEquivocation, e.Height, e.Round, e.ExistingHash, e.NewHash)
}

// Unwrap returns ErrBFTEquivocation so errors.Is(err, ErrBFTEquivocation) works.
func (e *ProposerEquivocationError) Unwrap() error { return ErrBFTEquivocation }

// VoteType distinguishes pre-vote from pre-commit in BFT rounds.
type VoteType string

const (
	VotePreVote   VoteType = "prevote"
	VotePreCommit VoteType = "precommit"
)

// BlockVote represents a single validator's vote on a proposed block.
type BlockVote struct {
	Validator string    `json:"validator"`
	BlockHash string    `json:"block_hash"`
	Height    uint64    `json:"height"`
	Round     uint32    `json:"round"`
	Type      VoteType  `json:"type"`
	Timestamp time.Time `json:"timestamp"`
}

// ConsensusStatus tracks the state of a single consensus round.
type ConsensusStatus string

const (
	StatusProposed  ConsensusStatus = "proposed"
	StatusPreVoted  ConsensusStatus = "prevoted"
	StatusCommitted ConsensusStatus = "committed"
	StatusFailed    ConsensusStatus = "failed"
)

// ConsensusRound tracks votes for a single block at a given height/round.
type ConsensusRound struct {
	Height    uint64          `json:"height"`
	Round     uint32          `json:"round"`
	Proposer  string          `json:"proposer"`
	BlockHash string          `json:"block_hash"`
	// LockedBlockHash is the value locked by a ⅔ prevote polka (may differ from
	// BlockHash if the round is equivocating; empty until a prevote quorum exists).
	LockedBlockHash string          `json:"locked_block_hash,omitempty"`
	Status          ConsensusStatus `json:"status"`
	PreVotes        []BlockVote     `json:"prevotes"`
	Commits         []BlockVote     `json:"commits"`
	StartTime       time.Time       `json:"start_time"`
	EndTime         time.Time       `json:"end_time,omitempty"`
	Deadline        time.Time       `json:"deadline,omitempty"` // round timeout (set when proposed)
}

// ConsensusConfig tunes the BFT parameters.
type ConsensusConfig struct {
	QuorumFraction float64       // fraction of total stake needed (typically 2/3)
	RoundTimeout   time.Duration // max duration before round fails
	MaxRounds      uint32        // max rounds per height before giving up
}

// DefaultConsensusConfig returns standard BFT defaults.
func DefaultConsensusConfig() ConsensusConfig {
	return ConsensusConfig{
		QuorumFraction: 2.0 / 3.0,
		RoundTimeout:   30 * time.Second,
		MaxRounds:      5,
	}
}

// BFTConsensus implements simplified Tendermint-style BFT block voting.
type BFTConsensus struct {
	mu         sync.RWMutex
	validators *ValidatorSet
	cfg        ConsensusConfig
	rounds     map[uint64]*ConsensusRound // height -> current round
	committed  map[uint64]*ConsensusRound // height -> committed round
	// nextRound is the next round index to use at each height after a timeout/failure.
	nextRound map[uint64]uint32
	// carryPrevoteLock seeds LockedBlockHash on round>0 after a prior round ended with a lock
	// (timeout / fail) without commit — POL-style carry between rounds at the same height.
	carryPrevoteLock map[uint64]string
}

// NewBFTConsensus creates a consensus engine backed by the given validator set.
func NewBFTConsensus(validators *ValidatorSet, cfg ConsensusConfig) *BFTConsensus {
	if cfg.QuorumFraction <= 0 || cfg.QuorumFraction > 1 {
		cfg.QuorumFraction = 2.0 / 3.0
	}
	return &BFTConsensus{
		validators:       validators,
		cfg:              cfg,
		rounds:           make(map[uint64]*ConsensusRound),
		committed:        make(map[uint64]*ConsensusRound),
		nextRound:        make(map[uint64]uint32),
		carryPrevoteLock: make(map[uint64]string),
	}
}

// ProposerForRound selects the proposer for a given round by rotating through
// active validators ordered by stake (highest first), then address for stable ties.
func (bc *BFTConsensus) ProposerForRound(round uint32) (string, error) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.proposerForRoundLocked(round)
}

// Propose starts a new consensus round for a block at the given height.
// A higher round cannot begin until the current round is removed via commit,
// FailRound, or TickRoundTimeouts (implicit lock / vote set teardown).
func (bc *BFTConsensus) Propose(height uint64, round uint32, proposer, blockHash string) (*ConsensusRound, error) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if _, ok := bc.committed[height]; ok {
		return nil, fmt.Errorf("height %d already committed", height)
	}

	if existing, ok := bc.rounds[height]; ok {
		switch {
		case round < existing.Round:
			return nil, fmt.Errorf("round %d is behind active round %d at height %d", round, existing.Round, height)
		case round == existing.Round:
			if proposer == existing.Proposer && blockHash == existing.BlockHash {
				return existing, nil
			}
			if proposer == existing.Proposer && blockHash != existing.BlockHash {
				return nil, &ProposerEquivocationError{
					Height: height, Round: round, Proposer: proposer,
					ExistingHash: existing.BlockHash, NewHash: blockHash,
				}
			}
			return nil, fmt.Errorf("duplicate propose at height %d round %d", height, round)
		case round > existing.Round:
			return nil, fmt.Errorf("round %d still active at height %d; timeout or fail before proposing round %d", existing.Round, height, round)
		}
	}

	expected, err := bc.proposerForRoundLocked(round)
	if err != nil {
		return nil, err
	}
	if proposer != expected {
		return nil, fmt.Errorf("proposer mismatch for round %d: expected %s, got %s", round, expected, proposer)
	}

	// Verify proposer is an active validator
	v, ok := bc.validators.GetValidator(proposer)
	if !ok || v.Status != ValidatorActive {
		return nil, fmt.Errorf("proposer %s is not an active validator", proposer)
	}

	now := time.Now()
	deadline := now.Add(bc.cfg.RoundTimeout)
	if bc.cfg.RoundTimeout <= 0 {
		deadline = now.Add(30 * time.Second)
	}

	cr := &ConsensusRound{
		Height:    height,
		Round:     round,
		Proposer:  proposer,
		BlockHash: blockHash,
		Status:    StatusProposed,
		StartTime: now,
		Deadline:  deadline,
	}
	if round > 0 {
		if lock, ok := bc.carryPrevoteLock[height]; ok && lock != "" {
			cr.LockedBlockHash = lock
		}
	}
	bc.rounds[height] = cr
	return cr, nil
}

func (bc *BFTConsensus) proposerForRoundLocked(round uint32) (string, error) {
	active := bc.validators.ActiveValidators()
	if len(active) == 0 {
		return "", fmt.Errorf("no active validators")
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].Stake != active[j].Stake {
			return active[i].Stake > active[j].Stake
		}
		return active[i].Address < active[j].Address
	})
	idx := int(round) % len(active)
	return active[idx].Address, nil
}

// TickRoundTimeouts fails any active round whose deadline has passed and bumps
// the next round counter for that height (proposer rotation on re-propose).
func (bc *BFTConsensus) TickRoundTimeouts(now time.Time) []uint64 {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	var timedOut []uint64
	for height, cr := range bc.rounds {
		if cr.Deadline.IsZero() || now.Before(cr.Deadline) {
			continue
		}
		// Preserve prevote lock for the next round at this height (POL-style carry across timeouts).
		if cr.LockedBlockHash != "" {
			bc.carryPrevoteLock[height] = cr.LockedBlockHash
		}
		cr.Status = StatusFailed
		cr.EndTime = now
		delete(bc.rounds, height)
		bc.nextRound[height] = cr.Round + 1
		timedOut = append(timedOut, height)
	}
	return timedOut
}

// NextRoundAfterTimeout returns the round index to use after the last timeout at height.
func (bc *BFTConsensus) NextRoundAfterTimeout(height uint64) uint32 {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return bc.nextRound[height]
}

// ClearNextRound resets the escalated round counter (e.g. after successful commit elsewhere).
func (bc *BFTConsensus) ClearNextRound(height uint64) {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	delete(bc.nextRound, height)
}

// PreVote records a pre-vote from a validator.
func (bc *BFTConsensus) PreVote(height uint64, validator, blockHash string) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	cr, ok := bc.rounds[height]
	if !ok {
		return fmt.Errorf("no active round for height %d", height)
	}
	if cr.Status != StatusProposed && cr.Status != StatusPreVoted {
		return fmt.Errorf("round at height %d is in status %s, cannot prevote", height, cr.Status)
	}

	v, vOk := bc.validators.GetValidator(validator)
	if !vOk || v.Status != ValidatorActive {
		return fmt.Errorf("validator %s is not active", validator)
	}

	for _, vote := range cr.PreVotes {
		if vote.Validator == validator {
			return fmt.Errorf("validator %s already pre-voted at height %d", validator, height)
		}
	}

	cr.PreVotes = append(cr.PreVotes, BlockVote{
		Validator: validator,
		BlockHash: blockHash,
		Height:    height,
		Round:     cr.Round,
		Type:      VotePreVote,
		Timestamp: time.Now(),
	})

	if locked, ok := bc.pickLockedPrevoteHash(cr); ok {
		cr.LockedBlockHash = locked
		if locked == NilVoteHash {
			// Nil-polka clears carried proposal lock for subsequent rounds at this height.
			delete(bc.carryPrevoteLock, height)
		}
		if cr.Status == StatusProposed {
			cr.Status = StatusPreVoted
		}
	}

	return nil
}

// PreCommit records a pre-commit from a validator. Requires prevote quorum first.
func (bc *BFTConsensus) PreCommit(height uint64, validator, blockHash string) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	cr, ok := bc.rounds[height]
	if !ok {
		// Quorum can finalize and delete the active round while remaining validators still send precommits.
		if done, okDone := bc.committed[height]; okDone {
			if blockHash != done.BlockHash {
				return fmt.Errorf("late precommit at height %d for %q, committed %q", height, blockHash, done.BlockHash)
			}
			for _, vote := range done.Commits {
				if vote.Validator == validator {
					return nil
				}
			}
			return nil
		}
		return fmt.Errorf("no active round for height %d", height)
	}
	if cr.Status != StatusPreVoted {
		return fmt.Errorf("round at height %d needs prevote quorum before commits (status: %s)", height, cr.Status)
	}

	if err := bc.validatePreCommitAgainstLock(cr, blockHash); err != nil {
		return err
	}

	v, vOk := bc.validators.GetValidator(validator)
	if !vOk || v.Status != ValidatorActive {
		return fmt.Errorf("validator %s is not active", validator)
	}

	for _, vote := range cr.Commits {
		if vote.Validator == validator {
			return fmt.Errorf("validator %s already pre-committed at height %d", validator, height)
		}
	}

	cr.Commits = append(cr.Commits, BlockVote{
		Validator: validator,
		BlockHash: blockHash,
		Height:    height,
		Round:     cr.Round,
		Type:      VotePreCommit,
		Timestamp: time.Now(),
	})

	if bc.hasQuorum(cr.Commits, cr.BlockHash) {
		cr.Status = StatusCommitted
		cr.EndTime = time.Now()
		bc.committed[height] = cr
		delete(bc.rounds, height)
		delete(bc.nextRound, height)
		delete(bc.carryPrevoteLock, height)
	}

	return nil
}

// IsCommitted returns whether a block at the given height has been committed.
func (bc *BFTConsensus) IsCommitted(height uint64) bool {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	_, ok := bc.committed[height]
	return ok
}

// GetCommitted returns the committed round for a height.
func (bc *BFTConsensus) GetCommitted(height uint64) (*ConsensusRound, bool) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	cr, ok := bc.committed[height]
	return cr, ok
}

// GetRound returns the current active round for a height.
func (bc *BFTConsensus) GetRound(height uint64) (*ConsensusRound, bool) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	cr, ok := bc.rounds[height]
	return cr, ok
}

// FailRound marks the current round as failed (e.g. timeout).
func (bc *BFTConsensus) FailRound(height uint64) error {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	cr, ok := bc.rounds[height]
	if !ok {
		return fmt.Errorf("no active round for height %d", height)
	}
	if cr.LockedBlockHash != "" {
		bc.carryPrevoteLock[height] = cr.LockedBlockHash
	}
	cr.Status = StatusFailed
	cr.EndTime = time.Now()
	delete(bc.rounds, height)
	bc.nextRound[height] = cr.Round + 1
	return nil
}

// CommittedHeights returns all committed heights in ascending order.
func (bc *BFTConsensus) CommittedHeights() []uint64 {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	heights := make([]uint64, 0, len(bc.committed))
	for h := range bc.committed {
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	return heights
}

// CommittedCount returns how many blocks have been committed.
func (bc *BFTConsensus) CommittedCount() int {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	return len(bc.committed)
}

// hasQuorum checks if votes matching blockHash represent >= QuorumFraction of total active stake.
func (bc *BFTConsensus) hasQuorum(votes []BlockVote, blockHash string) bool {
	active := bc.validators.ActiveValidators()
	if len(active) == 0 {
		return false
	}

	var totalStake float64
	stakeMap := make(map[string]float64, len(active))
	for _, v := range active {
		totalStake += v.Stake
		stakeMap[v.Address] = v.Stake
	}

	var votedStake float64
	for _, vote := range votes {
		if vote.BlockHash == blockHash {
			votedStake += stakeMap[vote.Validator]
		}
	}

	return votedStake >= totalStake*bc.cfg.QuorumFraction
}

// pickLockedPrevoteHash returns the block hash locked by the current prevote set:
// the unique hash with ≥ quorum stake, preferring the proposed BlockHash on ties,
// then the hash with the greatest prevote stake, then lexicographically smallest.
func (bc *BFTConsensus) pickLockedPrevoteHash(cr *ConsensusRound) (string, bool) {
	active := bc.validators.ActiveValidators()
	if len(active) == 0 {
		return "", false
	}
	var totalStake float64
	stakeMap := make(map[string]float64, len(active))
	for _, v := range active {
		totalStake += v.Stake
		stakeMap[v.Address] = v.Stake
	}
	if totalStake <= 0 {
		return "", false
	}
	perHash := make(map[string]float64)
	for _, vote := range cr.PreVotes {
		perHash[vote.BlockHash] += stakeMap[vote.Validator]
	}
	threshold := totalStake * bc.cfg.QuorumFraction
	// Nil-polka unlock: if nil strictly outweighs the proposal on prevotes, lock nil so honest nodes
	// can precommit nil and move on without waiting for round timeout (Tendermint-style).
	nilStake := perHash[NilVoteHash]
	propStake := perHash[cr.BlockHash]
	if nilStake >= threshold && nilStake > propStake {
		return NilVoteHash, true
	}
	var candidates []string
	for h, stake := range perHash {
		if stake >= threshold {
			candidates = append(candidates, h)
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	for _, h := range candidates {
		if h == cr.BlockHash {
			return h, true
		}
	}
	best := candidates[0]
	bestStake := perHash[best]
	for _, h := range candidates[1:] {
		s := perHash[h]
		if s > bestStake || (s == bestStake && h < best) {
			best, bestStake = h, s
		}
	}
	return best, true
}

// validatePreCommitAgainstLock enforces Tendermint-style locking: after a prevote
// polka, precommits must be for the locked value or an explicit nil precommit.
func (bc *BFTConsensus) validatePreCommitAgainstLock(cr *ConsensusRound, blockHash string) error {
	if cr.LockedBlockHash == "" {
		return nil
	}
	if blockHash == NilVoteHash {
		return nil
	}
	if blockHash != cr.LockedBlockHash {
		return fmt.Errorf("precommit for %q does not match locked value %q", blockHash, cr.LockedBlockHash)
	}
	return nil
}
