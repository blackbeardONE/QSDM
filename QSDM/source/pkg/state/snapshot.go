package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Snapshot captures a point-in-time view of the node's state.
type Snapshot struct {
	Height    uint64                 `json:"height"`
	Hash      string                 `json:"hash"`
	Data      map[string]interface{} `json:"data"`
	CreatedAt time.Time              `json:"created_at"`
}

// SnapshotManager creates periodic snapshots and prunes old ones.
type SnapshotManager struct {
	mu           sync.RWMutex
	dir          string
	maxSnapshots int
	snapshots    []SnapshotMeta
	height       uint64
	stateFunc    func() map[string]interface{} // provides current state
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

// SnapshotMeta is the metadata for a stored snapshot (no embedded data).
type SnapshotMeta struct {
	Height    uint64    `json:"height"`
	Hash      string    `json:"hash"`
	File      string    `json:"file"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
}

// ManagerConfig configures snapshot behaviour.
type ManagerConfig struct {
	Dir          string        // directory to store snapshots
	MaxSnapshots int           // how many to retain
	Interval     time.Duration // auto-snapshot interval (0 = disabled)
}

// DefaultManagerConfig returns a sensible default.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		Dir:          "snapshots",
		MaxSnapshots: 10,
		Interval:     5 * time.Minute,
	}
}

// NewSnapshotManager creates a manager. stateFunc is called to capture current state.
func NewSnapshotManager(cfg ManagerConfig, stateFunc func() map[string]interface{}) *SnapshotManager {
	if cfg.MaxSnapshots <= 0 {
		cfg.MaxSnapshots = 10
	}
	sm := &SnapshotManager{
		dir:          cfg.Dir,
		maxSnapshots: cfg.MaxSnapshots,
		stateFunc:    stateFunc,
		stopCh:       make(chan struct{}),
	}
	os.MkdirAll(cfg.Dir, 0755)
	sm.loadIndex()
	return sm
}

// TakeSnapshot captures the current state, writes it to disk, and prunes old snapshots.
func (sm *SnapshotManager) TakeSnapshot() (*SnapshotMeta, error) {
	data := sm.stateFunc()

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.height++
	h := sm.height

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal state: %w", err)
	}

	hash := sha256.Sum256(raw)
	hashHex := hex.EncodeToString(hash[:])

	snap := Snapshot{
		Height:    h,
		Hash:      hashHex,
		Data:      data,
		CreatedAt: time.Now(),
	}

	filename := fmt.Sprintf("snap_%06d_%s.json", h, hashHex[:12])
	path := filepath.Join(sm.dir, filename)

	snapBytes, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	if err := os.WriteFile(path, snapBytes, 0644); err != nil {
		return nil, fmt.Errorf("write snapshot: %w", err)
	}

	info, _ := os.Stat(path)
	var size int64
	if info != nil {
		size = info.Size()
	}

	meta := SnapshotMeta{
		Height:    h,
		Hash:      hashHex,
		File:      filename,
		CreatedAt: snap.CreatedAt,
		SizeBytes: size,
	}
	sm.snapshots = append(sm.snapshots, meta)

	sm.pruneLocked()
	sm.saveIndex()

	return &meta, nil
}

// LoadSnapshot reads a snapshot from disk by height.
func (sm *SnapshotManager) LoadSnapshot(height uint64) (*Snapshot, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	for _, m := range sm.snapshots {
		if m.Height == height {
			return sm.readSnapshotFile(m.File)
		}
	}
	return nil, fmt.Errorf("snapshot at height %d not found", height)
}

// LatestSnapshot returns the most recent snapshot.
func (sm *SnapshotManager) LatestSnapshot() (*Snapshot, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if len(sm.snapshots) == 0 {
		return nil, fmt.Errorf("no snapshots available")
	}
	latest := sm.snapshots[len(sm.snapshots)-1]
	return sm.readSnapshotFile(latest.File)
}

// ListSnapshots returns metadata for all retained snapshots.
func (sm *SnapshotManager) ListSnapshots() []SnapshotMeta {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	out := make([]SnapshotMeta, len(sm.snapshots))
	copy(out, sm.snapshots)
	return out
}

// Prune removes old snapshots beyond the retention limit.
func (sm *SnapshotManager) Prune() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.pruneLocked()
}

func (sm *SnapshotManager) pruneLocked() int {
	if len(sm.snapshots) <= sm.maxSnapshots {
		return 0
	}
	sort.Slice(sm.snapshots, func(i, j int) bool { return sm.snapshots[i].Height < sm.snapshots[j].Height })
	toRemove := len(sm.snapshots) - sm.maxSnapshots
	pruned := 0
	for _, m := range sm.snapshots[:toRemove] {
		path := filepath.Join(sm.dir, m.File)
		os.Remove(path)
		pruned++
	}
	sm.snapshots = sm.snapshots[toRemove:]
	return pruned
}

// Start begins the auto-snapshot loop.
func (sm *SnapshotManager) Start(interval time.Duration) {
	if interval <= 0 {
		return
	}
	sm.wg.Add(1)
	go func() {
		defer sm.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-sm.stopCh:
				return
			case <-ticker.C:
				sm.TakeSnapshot()
			}
		}
	}()
}

// Stop halts auto-snapshotting.
func (sm *SnapshotManager) Stop() {
	close(sm.stopCh)
	sm.wg.Wait()
}

// ErrSnapshotIntegrity is returned by LoadSnapshot / LatestSnapshot when
// the SHA-256 of the snapshot's Data field does not match the Hash
// embedded in the same file. Audit row store-02 ("Snapshot hash
// verification"): a snapshot tampered on disk (bit-rot, malicious
// modification, partial write) is rejected at load time instead of
// being silently fed back into the chain replay path.
//
// Wrapped by fmt.Errorf in readSnapshotFile so callers can use
// errors.Is(err, ErrSnapshotIntegrity) to special-case integrity
// failures from generic I/O failures.
var ErrSnapshotIntegrity = fmt.Errorf("snapshot integrity check failed")

func (sm *SnapshotManager) readSnapshotFile(filename string) (*Snapshot, error) {
	path := filepath.Join(sm.dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read snapshot file: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	// Integrity verification (audit row store-02). Re-marshal the
	// Data field with the SAME settings TakeSnapshot used
	// (json.MarshalIndent(data, "", "  ")) and SHA-256 the result.
	// MUST match the Hash field saved in the snapshot file. If it
	// does not, the file has been tampered with or corrupted
	// between TakeSnapshot and now — refuse to feed it into the
	// state-restore pipeline.
	//
	// The Hash field MUST be a non-empty hex string for a snapshot
	// produced by TakeSnapshot. An empty Hash on a snapshot file is
	// treated as a tampered-by-stripping case (someone wrote a
	// snapshot without going through TakeSnapshot) — also rejected.
	if snap.Hash == "" {
		return nil, fmt.Errorf("%w: snapshot %q has empty Hash field; not produced by TakeSnapshot",
			ErrSnapshotIntegrity, filename)
	}
	expected, err := json.MarshalIndent(snap.Data, "", "  ")
	if err != nil {
		// Re-marshaling a map[string]interface{} that just round-tripped
		// through json.Unmarshal should not be able to fail. Treat as
		// an integrity failure (we can't verify, so we can't trust).
		return nil, fmt.Errorf("%w: snapshot %q: re-marshal for hash check: %v",
			ErrSnapshotIntegrity, filename, err)
	}
	gotBytes := sha256.Sum256(expected)
	got := hex.EncodeToString(gotBytes[:])
	if got != snap.Hash {
		return nil, fmt.Errorf("%w: snapshot %q hash mismatch: stored=%s recomputed=%s (tampered or corrupted)",
			ErrSnapshotIntegrity, filename, snap.Hash, got)
	}

	return &snap, nil
}

const indexFile = "_index.json"

func (sm *SnapshotManager) saveIndex() {
	data, err := json.MarshalIndent(sm.snapshots, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(sm.dir, indexFile), data, 0644)
}

func (sm *SnapshotManager) loadIndex() {
	path := filepath.Join(sm.dir, indexFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var metas []SnapshotMeta
	if err := json.Unmarshal(data, &metas); err != nil {
		return
	}
	sm.snapshots = metas
	for _, m := range metas {
		if m.Height > sm.height {
			sm.height = m.Height
		}
	}
}
