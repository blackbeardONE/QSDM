package chain

import (
	"fmt"
	"sync"
	"time"
)

// FinalityStatus describes how final a block is.
type FinalityStatus string

const (
	FinalityPending   FinalityStatus = "pending"
	FinalityConfirmed FinalityStatus = "confirmed"
	FinalityFinalized FinalityStatus = "finalized"
)

// FinalityConfig configures the finality gadget.
type FinalityConfig struct {
	ConfirmationDepth int           // blocks after which a block is "confirmed"
	FinalityDepth     int           // blocks after which a block is "finalized" (irreversible)
	ReorgLimit        int           // max reorg depth allowed (reject deeper reorgs)
	FinalizeInterval  time.Duration // how often to sweep and finalize
}

// DefaultFinalityConfig returns sensible defaults.
func DefaultFinalityConfig() FinalityConfig {
	return FinalityConfig{
		ConfirmationDepth: 6,
		FinalityDepth:     20,
		ReorgLimit:        50,
		FinalizeInterval:  10 * time.Second,
	}
}

// FinalityRecord tracks the finality state of a single block.
type FinalityRecord struct {
	Height        uint64         `json:"height"`
	Hash          string         `json:"hash"`
	StateRoot     string         `json:"state_root,omitempty"`
	Status        FinalityStatus `json:"status"`
	Confirmations int            `json:"confirmations"`
	FinalizedAt   time.Time      `json:"finalized_at,omitempty"`
}

// FinalityGadget tracks confirmation depth and finality for blocks.
type FinalityGadget struct {
	mu            sync.RWMutex
	records       map[uint64]*FinalityRecord // height -> record
	config        FinalityConfig
	chainTip      uint64
	lastFinalized uint64
	polFollower   *PolFollower
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

// NewFinalityGadget creates a finality tracker.
func NewFinalityGadget(cfg FinalityConfig) *FinalityGadget {
	if cfg.ConfirmationDepth <= 0 {
		cfg.ConfirmationDepth = 6
	}
	if cfg.FinalityDepth <= 0 {
		cfg.FinalityDepth = 20
	}
	if cfg.ReorgLimit <= 0 {
		cfg.ReorgLimit = 50
	}
	return &FinalityGadget{
		records: make(map[uint64]*FinalityRecord),
		config:  cfg,
		stopCh:  make(chan struct{}),
	}
}

// SetPolFollower attaches optional POL fork-choice policy for finalization (may be nil).
func (fg *FinalityGadget) SetPolFollower(p *PolFollower) {
	if fg == nil {
		return
	}
	fg.mu.Lock()
	defer fg.mu.Unlock()
	fg.polFollower = p
}

// TrackBlock registers a new block for finality tracking (block hash only).
func (fg *FinalityGadget) TrackBlock(height uint64, hash string) {
	fg.TrackBlockWithMeta(height, hash, "")
}

// TrackBlockWithMeta registers a block including state root for POL-anchored finality.
func (fg *FinalityGadget) TrackBlockWithMeta(height uint64, blockHash, stateRoot string) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	fg.records[height] = &FinalityRecord{
		Height:    height,
		Hash:      blockHash,
		StateRoot: stateRoot,
		Status:    FinalityPending,
	}
	if height > fg.chainTip {
		fg.chainTip = height
	}
}

// UpdateTip updates the chain tip and recalculates confirmations.
func (fg *FinalityGadget) UpdateTip(tipHeight uint64) (newlyConfirmed, newlyFinalized int) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	fg.chainTip = tipHeight

	for height, rec := range fg.records {
		if tipHeight >= height {
			rec.Confirmations = int(tipHeight - height)
		}

		if rec.Status == FinalityPending && rec.Confirmations >= fg.config.ConfirmationDepth {
			rec.Status = FinalityConfirmed
			newlyConfirmed++
		}

		if rec.Status == FinalityConfirmed && rec.Confirmations >= fg.config.FinalityDepth {
			if fg.polFollower != nil && !fg.polFollower.AllowFinalize(height, rec.StateRoot) {
				continue
			}
			rec.Status = FinalityFinalized
			rec.FinalizedAt = time.Now()
			newlyFinalized++
			if height > fg.lastFinalized {
				fg.lastFinalized = height
			}
		}
	}
	return
}

// GetStatus returns the finality status of a block at the given height.
func (fg *FinalityGadget) GetStatus(height uint64) (*FinalityRecord, bool) {
	fg.mu.RLock()
	defer fg.mu.RUnlock()
	rec, ok := fg.records[height]
	if !ok {
		return nil, false
	}
	cp := *rec
	return &cp, true
}

// IsFinalized returns true if the block at height is finalized.
func (fg *FinalityGadget) IsFinalized(height uint64) bool {
	fg.mu.RLock()
	defer fg.mu.RUnlock()
	rec, ok := fg.records[height]
	return ok && rec.Status == FinalityFinalized
}

// CheckReorg verifies that a proposed fork depth doesn't exceed the reorg limit.
func (fg *FinalityGadget) CheckReorg(forkHeight uint64) error {
	fg.mu.RLock()
	defer fg.mu.RUnlock()

	if fg.chainTip == 0 || forkHeight >= fg.chainTip {
		return nil
	}

	depth := fg.chainTip - forkHeight
	if int(depth) > fg.config.ReorgLimit {
		return fmt.Errorf("reorg depth %d exceeds limit %d", depth, fg.config.ReorgLimit)
	}

	if forkHeight <= fg.lastFinalized {
		return fmt.Errorf("cannot reorg past finalized height %d", fg.lastFinalized)
	}

	return nil
}

// LastFinalized returns the height of the last finalized block.
func (fg *FinalityGadget) LastFinalized() uint64 {
	fg.mu.RLock()
	defer fg.mu.RUnlock()
	return fg.lastFinalized
}

// PendingCount returns the number of blocks awaiting confirmation.
func (fg *FinalityGadget) PendingCount() int {
	fg.mu.RLock()
	defer fg.mu.RUnlock()
	count := 0
	for _, rec := range fg.records {
		if rec.Status == FinalityPending {
			count++
		}
	}
	return count
}

// FinalizedCount returns the number of finalized blocks.
func (fg *FinalityGadget) FinalizedCount() int {
	fg.mu.RLock()
	defer fg.mu.RUnlock()
	count := 0
	for _, rec := range fg.records {
		if rec.Status == FinalityFinalized {
			count++
		}
	}
	return count
}

// Start begins the background finalization sweep.
func (fg *FinalityGadget) Start(interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	fg.wg.Add(1)
	go func() {
		defer fg.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-fg.stopCh:
				return
			case <-ticker.C:
				fg.UpdateTip(fg.chainTip)
			}
		}
	}()
}

// Stop halts the background sweep.
func (fg *FinalityGadget) Stop() {
	close(fg.stopCh)
	fg.wg.Wait()
}
