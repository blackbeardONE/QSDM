package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// NilVoteHash is the canonical block hash value for explicit nil pre-vote / pre-commit
// (no vote for the proposed block), for lock-step gossip and light-client summaries.
const NilVoteHash = "<nil>"

// PreVoteNil records a nil pre-vote for the active round at height.
func (bc *BFTConsensus) PreVoteNil(height uint64, validator string) error {
	return bc.PreVote(height, validator, NilVoteHash)
}

// PreCommitNil records a nil pre-commit for the active round at height.
func (bc *BFTConsensus) PreCommitNil(height uint64, validator string) error {
	return bc.PreCommit(height, validator, NilVoteHash)
}

// RoundCertificate is a compact, verifiable summary of a committed round for light clients.
type RoundCertificate struct {
	Height        uint64   `json:"height"`
	Round         uint32   `json:"round"`
	Proposer      string   `json:"proposer"`
	BlockHash     string   `json:"block_hash"`
	CommitDigest  string   `json:"commit_digest"` // SHA-256 of canonical commit payload
	ValidatorSet  []string `json:"validators"`    // active validators at certification time (addresses)
	CommitCount    int `json:"commit_count"`
	NilCommitCount int `json:"nil_commit_count"`
}

// BuildRoundCertificate builds a certificate from the committed round at height.
func (bc *BFTConsensus) BuildRoundCertificate(height uint64) (*RoundCertificate, error) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	cr, ok := bc.committed[height]
	if !ok || cr == nil {
		return nil, fmt.Errorf("no committed round at height %d", height)
	}
	active := bc.validators.ActiveValidators()
	vals := make([]string, 0, len(active))
	for _, v := range active {
		vals = append(vals, v.Address)
	}
	sort.Strings(vals)

	type commitWire struct {
		V string `json:"v"`
		H string `json:"h"`
	}
	wires := make([]commitWire, 0, len(cr.Commits))
	nilC := 0
	for _, c := range cr.Commits {
		wires = append(wires, commitWire{V: c.Validator, H: c.BlockHash})
		if c.BlockHash == NilVoteHash {
			nilC++
		}
	}
	sort.Slice(wires, func(i, j int) bool {
		if wires[i].V != wires[j].V {
			return wires[i].V < wires[j].V
		}
		return wires[i].H < wires[j].H
	})
	canonical, _ := json.Marshal(wires)
	sum := sha256.Sum256(canonical)

	return &RoundCertificate{
		Height:         cr.Height,
		Round:          cr.Round,
		Proposer:       cr.Proposer,
		BlockHash:      cr.BlockHash,
		CommitDigest:   hex.EncodeToString(sum[:]),
		ValidatorSet:   vals,
		CommitCount:    len(cr.Commits),
		NilCommitCount: nilC,
	}, nil
}

// BuildPrevoteLockProof builds a portable POL bundle for the active or committed round at height.
// It requires a non-empty LockedBlockHash (⅔ prevote polka already observed).
func (bc *BFTConsensus) BuildPrevoteLockProof(height uint64) (*PrevoteLockProof, error) {
	bc.mu.RLock()
	defer bc.mu.RUnlock()
	cr := bc.rounds[height]
	if cr == nil {
		cr = bc.committed[height]
	}
	if cr == nil {
		return nil, fmt.Errorf("no round for height %d", height)
	}
	if cr.LockedBlockHash == "" {
		return nil, fmt.Errorf("no prevote lock at height %d", height)
	}
	prevotes := make([]BlockVote, len(cr.PreVotes))
	copy(prevotes, cr.PreVotes)
	var carried string
	if lock, ok := bc.carryPrevoteLock[height]; ok {
		carried = lock
	}
	return &PrevoteLockProof{
		Height:          height,
		Round:           cr.Round,
		LockedBlockHash: cr.LockedBlockHash,
		CarriedFromLock: carried,
		Prevotes:        prevotes,
	}, nil
}
