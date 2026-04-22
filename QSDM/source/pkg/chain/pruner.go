package chain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PrunerConfig configures chain pruning behaviour.
type PrunerConfig struct {
	KeepBlocks    int           // number of recent full blocks to retain
	ArchiveDir    string        // directory for archived block data
	PruneInterval time.Duration // how often the background pruner runs
}

// DefaultPrunerConfig returns sensible defaults.
func DefaultPrunerConfig() PrunerConfig {
	return PrunerConfig{
		KeepBlocks:    100,
		ArchiveDir:    "archive",
		PruneInterval: 5 * time.Minute,
	}
}

// ArchivedBlock stores minimal data: header only (txs stripped).
type ArchivedBlock struct {
	Header    BlockHeader `json:"header"`
	TotalFees float64     `json:"total_fees"`
	GasUsed   int64       `json:"gas_used"`
}

// ChainPruner archives old block data while preserving headers for SPV.
type ChainPruner struct {
	mu          sync.RWMutex
	headers     []BlockHeader          // all headers retained (append-only)
	fullBlocks  map[uint64]*Block      // recent full blocks
	archived    map[uint64]string      // height -> archive filename
	keepBlocks  int
	archiveDir  string
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewChainPruner creates a pruner.
func NewChainPruner(cfg PrunerConfig) *ChainPruner {
	if cfg.KeepBlocks <= 0 {
		cfg.KeepBlocks = 100
	}
	if cfg.ArchiveDir != "" {
		os.MkdirAll(cfg.ArchiveDir, 0755)
	}
	return &ChainPruner{
		fullBlocks: make(map[uint64]*Block),
		archived:   make(map[uint64]string),
		keepBlocks: cfg.KeepBlocks,
		archiveDir: cfg.ArchiveDir,
		stopCh:     make(chan struct{}),
	}
}

// AddBlock ingests a new block. Call this after ProduceBlock.
func (cp *ChainPruner) AddBlock(block *Block) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	cp.headers = append(cp.headers, block.Header())
	cp.fullBlocks[block.Height] = block
}

// Prune archives old full blocks beyond the retention window.
func (cp *ChainPruner) Prune() (pruned int, err error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if len(cp.headers) <= cp.keepBlocks {
		return 0, nil
	}

	cutoff := cp.headers[len(cp.headers)-1].Height - uint64(cp.keepBlocks)

	for height, block := range cp.fullBlocks {
		if height > cutoff {
			continue
		}
		if cp.archiveDir != "" {
			if archiveErr := cp.archiveBlock(block); archiveErr != nil {
				err = archiveErr
				continue
			}
		}
		delete(cp.fullBlocks, height)
		pruned++
	}
	return
}

// GetFullBlock returns a full block if still retained.
func (cp *ChainPruner) GetFullBlock(height uint64) (*Block, bool) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	b, ok := cp.fullBlocks[height]
	return b, ok
}

// GetHeader returns a header by height. Headers are never pruned.
func (cp *ChainPruner) GetHeader(height uint64) (*BlockHeader, bool) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	for i := range cp.headers {
		if cp.headers[i].Height == height {
			h := cp.headers[i]
			return &h, true
		}
	}
	return nil, false
}

// HeaderRange returns headers for the given range (inclusive).
func (cp *ChainPruner) HeaderRange(from, to uint64) []BlockHeader {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	var out []BlockHeader
	for i := range cp.headers {
		if cp.headers[i].Height >= from && cp.headers[i].Height <= to {
			out = append(out, cp.headers[i])
		}
	}
	return out
}

// IsPruned returns true if a block has been archived (tx data no longer available in memory).
func (cp *ChainPruner) IsPruned(height uint64) bool {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	_, inFull := cp.fullBlocks[height]
	_, inArchive := cp.archived[height]
	return !inFull && inArchive
}

// LoadArchived reads an archived block from disk (header + metadata only).
func (cp *ChainPruner) LoadArchived(height uint64) (*ArchivedBlock, error) {
	cp.mu.RLock()
	filename, ok := cp.archived[height]
	cp.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("block %d not in archive", height)
	}

	data, err := os.ReadFile(filepath.Join(cp.archiveDir, filename))
	if err != nil {
		return nil, err
	}
	var ab ArchivedBlock
	if err := json.Unmarshal(data, &ab); err != nil {
		return nil, err
	}
	return &ab, nil
}

// TotalHeaders returns how many headers have been stored.
func (cp *ChainPruner) TotalHeaders() int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return len(cp.headers)
}

// RetainedBlocks returns how many full blocks are in memory.
func (cp *ChainPruner) RetainedBlocks() int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return len(cp.fullBlocks)
}

// ArchivedCount returns how many blocks have been archived.
func (cp *ChainPruner) ArchivedCount() int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return len(cp.archived)
}

// Start begins the background pruning loop.
func (cp *ChainPruner) Start(interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	cp.wg.Add(1)
	go func() {
		defer cp.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-cp.stopCh:
				return
			case <-ticker.C:
				cp.Prune()
			}
		}
	}()
}

// Stop halts the background loop.
func (cp *ChainPruner) Stop() {
	close(cp.stopCh)
	cp.wg.Wait()
}

func (cp *ChainPruner) archiveBlock(block *Block) error {
	ab := ArchivedBlock{
		Header:    block.Header(),
		TotalFees: block.TotalFees,
		GasUsed:   block.GasUsed,
	}
	data, err := json.MarshalIndent(ab, "", "  ")
	if err != nil {
		return err
	}
	filename := fmt.Sprintf("block_%06d.json", block.Height)
	path := filepath.Join(cp.archiveDir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	cp.archived[block.Height] = filename
	return nil
}
