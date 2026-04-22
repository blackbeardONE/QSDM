package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// SyncStatus tracks the progress of a state sync operation.
type SyncStatus string

const (
	SyncIdle       SyncStatus = "idle"
	SyncRequesting SyncStatus = "requesting"
	SyncReceiving  SyncStatus = "receiving"
	SyncApplying   SyncStatus = "applying"
	SyncComplete   SyncStatus = "complete"
	SyncFailed     SyncStatus = "failed"
)

// SyncRequest is sent by a joining node to request a snapshot.
type SyncRequest struct {
	RequestID   string `json:"request_id"`
	FromHeight  uint64 `json:"from_height"`   // 0 = full sync from latest
	RequesterID string `json:"requester_id"`
}

// SyncResponse is sent by a serving node with a snapshot.
type SyncResponse struct {
	RequestID string                 `json:"request_id"`
	Height    uint64                 `json:"height"`
	Hash      string                 `json:"hash"`
	Data      map[string]interface{} `json:"data"`
	ChunkIdx  int                    `json:"chunk_idx"`
	TotalChunks int                  `json:"total_chunks"`
}

// SyncChunk is a piece of the state data for large snapshots.
type SyncChunk struct {
	RequestID string `json:"request_id"`
	ChunkIdx  int    `json:"chunk_idx"`
	Data      []byte `json:"data"`
	Hash      string `json:"hash"`
}

// SyncManager handles state sync for joining nodes.
type SyncManager struct {
	mu           sync.RWMutex
	snapManager  *SnapshotManager
	status       SyncStatus
	progress     float64 // 0.0 to 1.0
	lastSyncAt   time.Time
	peerID       string
	onApply      func(data map[string]interface{}) error
}

// NewSyncManager creates a sync manager.
func NewSyncManager(snapManager *SnapshotManager, peerID string, onApply func(data map[string]interface{}) error) *SyncManager {
	return &SyncManager{
		snapManager: snapManager,
		status:      SyncIdle,
		peerID:      peerID,
		onApply:     onApply,
	}
}

// Status returns the current sync state.
func (sm *SyncManager) Status() SyncStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.status
}

// Progress returns the sync progress (0.0 to 1.0).
func (sm *SyncManager) Progress() float64 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.progress
}

// HandleSyncRequest processes an incoming sync request from a joining peer.
// Returns a SyncResponse with the latest snapshot data.
func (sm *SyncManager) HandleSyncRequest(req SyncRequest) (*SyncResponse, error) {
	snap, err := sm.snapManager.LatestSnapshot()
	if err != nil {
		return nil, fmt.Errorf("no snapshot available: %w", err)
	}

	if req.FromHeight > 0 && snap.Height <= req.FromHeight {
		return nil, fmt.Errorf("requester already at height %d, latest is %d", req.FromHeight, snap.Height)
	}

	return &SyncResponse{
		RequestID:   req.RequestID,
		Height:      snap.Height,
		Hash:        snap.Hash,
		Data:        snap.Data,
		ChunkIdx:    0,
		TotalChunks: 1,
	}, nil
}

// ApplySync receives a sync response and applies it to local state.
func (sm *SyncManager) ApplySync(resp SyncResponse) error {
	sm.mu.Lock()
	sm.status = SyncReceiving
	sm.progress = 0.5
	sm.mu.Unlock()

	// Verify hash
	raw, err := json.MarshalIndent(resp.Data, "", "  ")
	if err != nil {
		sm.setFailed()
		return fmt.Errorf("marshal state: %w", err)
	}
	hash := sha256.Sum256(raw)
	computedHash := hex.EncodeToString(hash[:])

	if computedHash != resp.Hash {
		sm.setFailed()
		return fmt.Errorf("hash mismatch: expected %s, got %s", resp.Hash, computedHash)
	}

	sm.mu.Lock()
	sm.status = SyncApplying
	sm.progress = 0.75
	sm.mu.Unlock()

	if sm.onApply != nil {
		if err := sm.onApply(resp.Data); err != nil {
			sm.setFailed()
			return fmt.Errorf("apply state: %w", err)
		}
	}

	sm.mu.Lock()
	sm.status = SyncComplete
	sm.progress = 1.0
	sm.lastSyncAt = time.Now()
	sm.mu.Unlock()

	return nil
}

// CreateSyncRequest builds a request for the latest state.
func (sm *SyncManager) CreateSyncRequest(currentHeight uint64) SyncRequest {
	sm.mu.Lock()
	sm.status = SyncRequesting
	sm.progress = 0.1
	sm.mu.Unlock()

	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%d", sm.peerID, currentHeight, time.Now().UnixNano())))
	return SyncRequest{
		RequestID:   hex.EncodeToString(h[:16]),
		FromHeight:  currentHeight,
		RequesterID: sm.peerID,
	}
}

// Reset clears the sync state back to idle.
func (sm *SyncManager) Reset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.status = SyncIdle
	sm.progress = 0
}

// Info returns sync status information.
func (sm *SyncManager) Info() map[string]interface{} {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	info := map[string]interface{}{
		"status":   string(sm.status),
		"progress": sm.progress,
		"peer_id":  sm.peerID,
	}
	if !sm.lastSyncAt.IsZero() {
		info["last_sync_at"] = sm.lastSyncAt.Format(time.RFC3339)
	}
	return info
}

func (sm *SyncManager) setFailed() {
	sm.mu.Lock()
	sm.status = SyncFailed
	sm.mu.Unlock()
}
