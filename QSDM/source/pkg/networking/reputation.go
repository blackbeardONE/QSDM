package networking

import (
	"math"
	"sort"
	"sync"
	"time"
)

// PeerEvent categorises observable peer behaviour.
type PeerEvent int

const (
	EventValidBlock PeerEvent = iota
	EventInvalidBlock
	EventValidTx
	EventInvalidTx
	EventTimeout
	EventLatencyReport
	EventDisconnect
	EventProtocolViolation
)

// ReputationConfig tunes scoring weights and thresholds.
type ReputationConfig struct {
	ValidBlockWeight      float64       // reward for relaying a valid block
	InvalidBlockWeight    float64       // penalty for relaying an invalid block
	ValidTxWeight         float64       // reward for relaying a valid tx
	InvalidTxWeight       float64       // penalty for relaying an invalid tx
	TimeoutWeight         float64       // penalty for a timeout
	ProtocolViolWeight    float64       // penalty for protocol violations
	DecayInterval         time.Duration // how often scores decay
	DecayFactor           float64       // multiplier applied per interval (0 < x < 1 preserves direction)
	BanThreshold          float64       // score below which peer is banned
	InitialScore          float64       // starting score for new peers
	MaxScore              float64       // score cap
	MinScore              float64       // score floor (to prevent -inf spirals)
	LatencyPenaltyPerMs   float64       // score deduction per ms of excess latency
	LatencyBaselineMs     float64       // below this, latency is fine (no penalty)
}

// ReputationConfigForEvidence returns stricter penalties for consensus-evidence gossip,
// where malformed payloads are treated like protocol violations.
func ReputationConfigForEvidence() ReputationConfig {
	cfg := DefaultReputationConfig()
	cfg.InvalidTxWeight = -15
	cfg.ProtocolViolWeight = -150
	cfg.ValidTxWeight = 0.5
	return cfg
}

// DefaultReputationConfig returns conservative defaults.
func DefaultReputationConfig() ReputationConfig {
	return ReputationConfig{
		ValidBlockWeight:    10.0,
		InvalidBlockWeight:  -50.0,
		ValidTxWeight:       1.0,
		InvalidTxWeight:     -10.0,
		TimeoutWeight:       -5.0,
		ProtocolViolWeight:  -100.0,
		DecayInterval:       5 * time.Minute,
		DecayFactor:         0.95,
		BanThreshold:        -200,
		InitialScore:        100,
		MaxScore:            1000,
		MinScore:            -500,
		LatencyPenaltyPerMs: -0.01,
		LatencyBaselineMs:   200,
	}
}

// PeerRecord holds a single peer's reputation data.
type PeerRecord struct {
	PeerID       string    `json:"peer_id"`
	Score        float64   `json:"score"`
	ValidBlocks  int       `json:"valid_blocks"`
	InvalidBlks  int       `json:"invalid_blocks"`
	ValidTxs     int       `json:"valid_txs"`
	InvalidTxs   int       `json:"invalid_txs"`
	Timeouts     int       `json:"timeouts"`
	Violations   int       `json:"violations"`
	AvgLatencyMs float64   `json:"avg_latency_ms"`
	LastSeen     time.Time `json:"last_seen"`
	Banned       bool      `json:"banned"`
	BannedAt     time.Time `json:"banned_at,omitempty"`

	latencyCount int
	latencySum   float64
}

// ReputationTracker monitors peer behaviour and assigns reputation scores.
type ReputationTracker struct {
	mu     sync.RWMutex
	peers  map[string]*PeerRecord
	cfg    ReputationConfig
	stopCh chan struct{}
}

// NewReputationTracker creates a tracker with the given config.
func NewReputationTracker(cfg ReputationConfig) *ReputationTracker {
	return &ReputationTracker{
		peers:  make(map[string]*PeerRecord),
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// RecordEvent applies a behavioural event to a peer's reputation score.
// For EventLatencyReport, value is the observed latency in milliseconds.
func (rt *ReputationTracker) RecordEvent(peerID string, event PeerEvent, value float64) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rec := rt.getOrCreate(peerID)
	rec.LastSeen = time.Now()

	switch event {
	case EventValidBlock:
		rec.Score += rt.cfg.ValidBlockWeight
		rec.ValidBlocks++
	case EventInvalidBlock:
		rec.Score += rt.cfg.InvalidBlockWeight
		rec.InvalidBlks++
	case EventValidTx:
		rec.Score += rt.cfg.ValidTxWeight
		rec.ValidTxs++
	case EventInvalidTx:
		rec.Score += rt.cfg.InvalidTxWeight
		rec.InvalidTxs++
	case EventTimeout:
		rec.Score += rt.cfg.TimeoutWeight
		rec.Timeouts++
	case EventProtocolViolation:
		rec.Score += rt.cfg.ProtocolViolWeight
		rec.Violations++
	case EventLatencyReport:
		rec.latencyCount++
		rec.latencySum += value
		rec.AvgLatencyMs = rec.latencySum / float64(rec.latencyCount)
		if value > rt.cfg.LatencyBaselineMs {
			excess := value - rt.cfg.LatencyBaselineMs
			rec.Score += excess * rt.cfg.LatencyPenaltyPerMs
		}
	case EventDisconnect:
		rec.Score += rt.cfg.TimeoutWeight
	}

	rec.Score = math.Max(rt.cfg.MinScore, math.Min(rt.cfg.MaxScore, rec.Score))

	if rec.Score <= rt.cfg.BanThreshold && !rec.Banned {
		rec.Banned = true
		rec.BannedAt = time.Now()
	}
}

// GetScore returns the current score for a peer.
func (rt *ReputationTracker) GetScore(peerID string) float64 {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rec, ok := rt.peers[peerID]; ok {
		return rec.Score
	}
	return rt.cfg.InitialScore
}

// IsBanned returns whether a peer is currently banned.
func (rt *ReputationTracker) IsBanned(peerID string) bool {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	if rec, ok := rt.peers[peerID]; ok {
		return rec.Banned
	}
	return false
}

// GetPeer returns a copy of a peer's record.
func (rt *ReputationTracker) GetPeer(peerID string) (PeerRecord, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	rec, ok := rt.peers[peerID]
	if !ok {
		return PeerRecord{}, false
	}
	return *rec, true
}

// AllPeers returns all peer records sorted by score descending.
func (rt *ReputationTracker) AllPeers() []PeerRecord {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make([]PeerRecord, 0, len(rt.peers))
	for _, rec := range rt.peers {
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// TopPeers returns the N highest-scoring non-banned peers.
func (rt *ReputationTracker) TopPeers(n int) []PeerRecord {
	all := rt.AllPeers()
	var top []PeerRecord
	for _, p := range all {
		if !p.Banned {
			top = append(top, p)
			if len(top) >= n {
				break
			}
		}
	}
	return top
}

// BannedPeers returns all currently banned peers.
func (rt *ReputationTracker) BannedPeers() []PeerRecord {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	var banned []PeerRecord
	for _, rec := range rt.peers {
		if rec.Banned {
			banned = append(banned, *rec)
		}
	}
	return banned
}

// Unban lifts a ban for a specific peer and resets score to initial.
func (rt *ReputationTracker) Unban(peerID string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rec, ok := rt.peers[peerID]
	if !ok || !rec.Banned {
		return false
	}
	rec.Banned = false
	rec.BannedAt = time.Time{}
	rec.Score = rt.cfg.InitialScore
	return true
}

// PeerCount returns total tracked peers.
func (rt *ReputationTracker) PeerCount() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return len(rt.peers)
}

// DecayAll applies the decay factor to all peer scores, pulling them toward zero.
func (rt *ReputationTracker) DecayAll() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, rec := range rt.peers {
		rec.Score *= rt.cfg.DecayFactor
		rec.Score = math.Max(rt.cfg.MinScore, math.Min(rt.cfg.MaxScore, rec.Score))
	}
}

// Start launches a background goroutine that periodically decays scores.
func (rt *ReputationTracker) Start() {
	go func() {
		ticker := time.NewTicker(rt.cfg.DecayInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rt.DecayAll()
			case <-rt.stopCh:
				return
			}
		}
	}()
}

// Stop halts the background decay loop.
func (rt *ReputationTracker) Stop() {
	close(rt.stopCh)
}

func (rt *ReputationTracker) getOrCreate(peerID string) *PeerRecord {
	rec, ok := rt.peers[peerID]
	if !ok {
		rec = &PeerRecord{
			PeerID:   peerID,
			Score:    rt.cfg.InitialScore,
			LastSeen: time.Now(),
		}
		rt.peers[peerID] = rec
	}
	return rec
}
