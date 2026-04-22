package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// EvidenceType categorizes consensus faults.
type EvidenceType string

const (
	EvidenceEquivocation EvidenceType = "equivocation"
	EvidenceInvalidVote  EvidenceType = "invalid_vote"
	// EvidenceForkWitness records conflicting full blocks at the same height (no validator slash).
	EvidenceForkWitness EvidenceType = "fork_witness"
)

// ConsensusEvidence represents slashing evidence for a validator.
type ConsensusEvidence struct {
	Type        EvidenceType `json:"type"`
	Validator   string       `json:"validator"`
	Height      uint64       `json:"height"`
	Round       uint32       `json:"round"`
	BlockHashes []string     `json:"block_hashes,omitempty"`
	Details     string       `json:"details,omitempty"`
	Timestamp   time.Time    `json:"timestamp"`
}

// EvidenceRecord stores processed evidence and outcome.
type EvidenceRecord struct {
	ID         string             `json:"id"`
	Evidence   ConsensusEvidence  `json:"evidence"`
	Processed  bool               `json:"processed"`
	SlashEvent *SlashEvent        `json:"slash_event,omitempty"`
	Error      string             `json:"error,omitempty"`
	ProcessedAt time.Time         `json:"processed_at,omitempty"`
}

// EvidenceManager handles evidence deduplication, persistence-in-memory, and slashing.
type EvidenceManager struct {
	mu      sync.RWMutex
	vs      *ValidatorSet
	staking *StakingLedger
	records map[string]*EvidenceRecord
	order   []string
}

// SubmitEvidenceBestEffort runs Process and ignores duplicate-evidence errors (for gossip-driven paths).
func (em *EvidenceManager) SubmitEvidenceBestEffort(ev ConsensusEvidence) {
	if em == nil {
		return
	}
	_, err := em.Process(ev)
	if err == nil {
		return
	}
	if strings.Contains(strings.ToLower(err.Error()), "duplicate evidence") {
		return
	}
}

// NewEvidenceManager creates evidence handler bound to validator set.
func NewEvidenceManager(vs *ValidatorSet) *EvidenceManager {
	return &EvidenceManager{
		vs:      vs,
		records: make(map[string]*EvidenceRecord),
	}
}

// SetStakingLedger optionally ties delegated stake to consensus slashing.
func (em *EvidenceManager) SetStakingLedger(sl *StakingLedger) {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.staking = sl
}

// Process validates and applies evidence, including automatic slashing.
func (em *EvidenceManager) Process(ev ConsensusEvidence) (*EvidenceRecord, error) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	id := evidenceID(ev)

	em.mu.Lock()
	if existing, ok := em.records[id]; ok {
		em.mu.Unlock()
		return existing, fmt.Errorf("duplicate evidence: %s", id)
	}
	rec := &EvidenceRecord{ID: id, Evidence: ev}
	em.records[id] = rec
	em.order = append(em.order, id)
	em.mu.Unlock()

	if err := validateEvidence(ev); err != nil {
		em.mu.Lock()
		rec.Error = err.Error()
		rec.ProcessedAt = time.Now()
		em.mu.Unlock()
		return rec, err
	}

	if ev.Type == EvidenceForkWitness {
		em.mu.Lock()
		rec.Processed = true
		rec.ProcessedAt = time.Now()
		em.mu.Unlock()
		return rec, nil
	}

	reason := SlashInvalidBlock
	if ev.Type == EvidenceEquivocation {
		reason = SlashDoubleSign
	}
	slashEvent, err := em.vs.Slash(ev.Validator, reason)

	em.mu.RLock()
	sl := em.staking
	em.mu.RUnlock()
	if err == nil && sl != nil {
		cfg := DefaultValidatorSetConfig()
		frac := cfg.SlashFraction
		if reason == SlashDowntime {
			frac = cfg.DowntimeSlash
		}
		sl.SlashDelegated(ev.Validator, frac)
	}

	em.mu.Lock()
	defer em.mu.Unlock()
	rec.Processed = err == nil
	rec.ProcessedAt = time.Now()
	if err != nil {
		rec.Error = err.Error()
		return rec, err
	}
	rec.SlashEvent = slashEvent
	return rec, nil
}

// Get returns processed evidence record by ID.
func (em *EvidenceManager) Get(id string) (*EvidenceRecord, bool) {
	em.mu.RLock()
	defer em.mu.RUnlock()
	r, ok := em.records[id]
	return r, ok
}

// List returns all evidence records in insertion order.
func (em *EvidenceManager) List() []EvidenceRecord {
	em.mu.RLock()
	defer em.mu.RUnlock()
	out := make([]EvidenceRecord, 0, len(em.order))
	for _, id := range em.order {
		if r, ok := em.records[id]; ok {
			out = append(out, *r)
		}
	}
	return out
}

// Stats returns summary counts.
func (em *EvidenceManager) Stats() map[string]int {
	em.mu.RLock()
	defer em.mu.RUnlock()
	stats := map[string]int{"total": len(em.records), "processed": 0, "failed": 0}
	for _, r := range em.records {
		if r.Processed {
			stats["processed"]++
		} else if r.Error != "" {
			stats["failed"]++
		}
	}
	return stats
}

func validateEvidence(ev ConsensusEvidence) error {
	if ev.Type != EvidenceForkWitness && ev.Validator == "" {
		return fmt.Errorf("validator is required")
	}
	switch ev.Type {
	case EvidenceEquivocation:
		if len(ev.BlockHashes) < 2 {
			return fmt.Errorf("equivocation evidence requires at least 2 block hashes")
		}
		seen := map[string]bool{}
		for _, h := range ev.BlockHashes {
			seen[h] = true
		}
		if len(seen) < 2 {
			return fmt.Errorf("equivocation requires conflicting block hashes")
		}
	case EvidenceInvalidVote:
		if ev.Details == "" {
			return fmt.Errorf("invalid_vote evidence requires details")
		}
	case EvidenceForkWitness:
		if ev.Details == "" {
			return fmt.Errorf("fork_witness requires details")
		}
		if len(ev.BlockHashes) < 2 {
			return fmt.Errorf("fork_witness requires two block hashes")
		}
		seen := map[string]bool{}
		for _, h := range ev.BlockHashes {
			seen[h] = true
		}
		if len(seen) < 2 {
			return fmt.Errorf("fork_witness requires two distinct block hashes")
		}
	default:
		return fmt.Errorf("unsupported evidence type: %s", ev.Type)
	}
	return nil
}

// StableEvidenceID returns the canonical deduplication ID for evidence (wire + gossip).
func StableEvidenceID(ev ConsensusEvidence) string {
	return evidenceID(ev)
}

func evidenceID(ev ConsensusEvidence) string {
	hashes := append([]string(nil), ev.BlockHashes...)
	sort.Strings(hashes)
	data := fmt.Sprintf("%s|%s|%d|%d|%v|%s", ev.Type, ev.Validator, ev.Height, ev.Round, hashes, ev.Details)
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

