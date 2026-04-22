package networking

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// PEXPeer describes a known peer for exchange.
type PEXPeer struct {
	ID         string    `json:"id"`
	Addresses  []string  `json:"addresses"`
	LastSeen   time.Time `json:"last_seen"`
	Source     string    `json:"source"` // "manual", "pex", "bootstrap", "inbound"
	Reachable  bool      `json:"reachable"`
	FailCount  int       `json:"fail_count"`
}

// PEXMessage is the wire format for peer exchange.
type PEXMessage struct {
	Type    PEXType    `json:"type"`
	Sender  string     `json:"sender"`
	Peers   []PEXPeer  `json:"peers,omitempty"`
	MaxPeers int       `json:"max_peers,omitempty"`
}

// PEXType distinguishes request from response.
type PEXType string

const (
	PEXRequest  PEXType = "pex_request"
	PEXResponse PEXType = "pex_response"
)

// PEXConfig tunes the peer exchange protocol.
type PEXConfig struct {
	MaxKnownPeers    int           // maximum peers to track
	MaxExchangePeers int           // max peers to share per response
	StaleTimeout     time.Duration // remove peers not seen for this long
	ExchangeInterval time.Duration // how often to request peers
	MaxFailCount     int           // mark unreachable after N failures
}

// DefaultPEXConfig returns conservative defaults.
func DefaultPEXConfig() PEXConfig {
	return PEXConfig{
		MaxKnownPeers:    500,
		MaxExchangePeers: 20,
		StaleTimeout:     1 * time.Hour,
		ExchangeInterval: 5 * time.Minute,
		MaxFailCount:     5,
	}
}

// PEXManager manages peer discovery through structured peer exchange.
type PEXManager struct {
	mu     sync.RWMutex
	selfID string
	peers  map[string]*PEXPeer
	cfg    PEXConfig
	stopCh chan struct{}
	onNewPeer func(PEXPeer) // callback for newly discovered peers
}

// NewPEXManager creates a PEX manager for the given local node ID.
func NewPEXManager(selfID string, cfg PEXConfig) *PEXManager {
	if cfg.MaxKnownPeers <= 0 {
		cfg.MaxKnownPeers = 500
	}
	if cfg.MaxExchangePeers <= 0 {
		cfg.MaxExchangePeers = 20
	}
	return &PEXManager{
		selfID: selfID,
		peers:  make(map[string]*PEXPeer),
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// OnNewPeer registers a callback invoked when a new peer is discovered.
func (pm *PEXManager) OnNewPeer(fn func(PEXPeer)) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.onNewPeer = fn
}

// AddPeer adds or updates a known peer.
func (pm *PEXManager) AddPeer(info PEXPeer) bool {
	if info.ID == pm.selfID || info.ID == "" {
		return false
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	existing, ok := pm.peers[info.ID]
	if ok {
		existing.LastSeen = info.LastSeen
		if len(info.Addresses) > 0 {
			existing.Addresses = mergeAddresses(existing.Addresses, info.Addresses)
		}
		existing.Reachable = true
		existing.FailCount = 0
		return false
	}

	// Evict oldest if at capacity
	if len(pm.peers) >= pm.cfg.MaxKnownPeers {
		pm.evictOldest()
	}

	if info.LastSeen.IsZero() {
		info.LastSeen = time.Now()
	}
	info.Reachable = true
	pm.peers[info.ID] = &info

	if pm.onNewPeer != nil {
		go pm.onNewPeer(info)
	}

	return true
}

// RemovePeer removes a peer from the known set.
func (pm *PEXManager) RemovePeer(id string) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if _, ok := pm.peers[id]; ok {
		delete(pm.peers, id)
		return true
	}
	return false
}

// MarkFailed increments a peer's failure count and marks unreachable if threshold exceeded.
func (pm *PEXManager) MarkFailed(id string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	p, ok := pm.peers[id]
	if !ok {
		return
	}
	p.FailCount++
	if p.FailCount >= pm.cfg.MaxFailCount {
		p.Reachable = false
	}
}

// MarkReachable resets a peer's failure state.
func (pm *PEXManager) MarkReachable(id string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	p, ok := pm.peers[id]
	if !ok {
		return
	}
	p.Reachable = true
	p.FailCount = 0
	p.LastSeen = time.Now()
}

// BuildRequest creates a PEX request message.
func (pm *PEXManager) BuildRequest() PEXMessage {
	return PEXMessage{
		Type:     PEXRequest,
		Sender:   pm.selfID,
		MaxPeers: pm.cfg.MaxExchangePeers,
	}
}

// BuildResponse creates a PEX response with the best known reachable peers.
func (pm *PEXManager) BuildResponse(requesterID string) PEXMessage {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var candidates []PEXPeer
	for _, p := range pm.peers {
		if p.ID == requesterID || !p.Reachable {
			continue
		}
		candidates = append(candidates, *p)
	}

	// Sort by last seen (most recent first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].LastSeen.After(candidates[j].LastSeen)
	})

	n := pm.cfg.MaxExchangePeers
	if n > len(candidates) {
		n = len(candidates)
	}

	return PEXMessage{
		Type:   PEXResponse,
		Sender: pm.selfID,
		Peers:  candidates[:n],
	}
}

// HandleMessage processes an incoming PEX message.
// Returns a response message (only for requests) and count of new peers learned.
func (pm *PEXManager) HandleMessage(msg PEXMessage) (*PEXMessage, int) {
	switch msg.Type {
	case PEXRequest:
		resp := pm.BuildResponse(msg.Sender)
		// Also learn the sender
		pm.AddPeer(PEXPeer{ID: msg.Sender, Source: "pex", LastSeen: time.Now()})
		return &resp, 0

	case PEXResponse:
		newCount := 0
		for _, p := range msg.Peers {
			p.Source = "pex"
			if pm.AddPeer(p) {
				newCount++
			}
		}
		return nil, newCount

	default:
		return nil, 0
	}
}

// GetPeer returns a known peer by ID.
func (pm *PEXManager) GetPeer(id string) (PEXPeer, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	p, ok := pm.peers[id]
	if !ok {
		return PEXPeer{}, false
	}
	return *p, true
}

// AllPeers returns all known peers sorted by last seen (most recent first).
func (pm *PEXManager) AllPeers() []PEXPeer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make([]PEXPeer, 0, len(pm.peers))
	for _, p := range pm.peers {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out
}

// ReachablePeers returns only reachable peers.
func (pm *PEXManager) ReachablePeers() []PEXPeer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	var out []PEXPeer
	for _, p := range pm.peers {
		if p.Reachable {
			out = append(out, *p)
		}
	}
	return out
}

// PeerCount returns the total number of known peers.
func (pm *PEXManager) PeerCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.peers)
}

// ReachableCount returns the number of reachable peers.
func (pm *PEXManager) ReachableCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	var n int
	for _, p := range pm.peers {
		if p.Reachable {
			n++
		}
	}
	return n
}

// EvictStale removes peers not seen within StaleTimeout.
func (pm *PEXManager) EvictStale() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	cutoff := time.Now().Add(-pm.cfg.StaleTimeout)
	var evicted int
	for id, p := range pm.peers {
		if p.LastSeen.Before(cutoff) {
			delete(pm.peers, id)
			evicted++
		}
	}
	return evicted
}

// Start begins a background loop for periodic stale eviction.
func (pm *PEXManager) Start() {
	go func() {
		ticker := time.NewTicker(pm.cfg.StaleTimeout / 4)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pm.EvictStale()
			case <-pm.stopCh:
				return
			}
		}
	}()
}

// Stop halts the background loop.
func (pm *PEXManager) Stop() {
	close(pm.stopCh)
}

// Encode serializes a PEX message to JSON.
func (msg *PEXMessage) Encode() ([]byte, error) {
	return json.Marshal(msg)
}

// DecodePEXMessage deserializes a PEX message from JSON.
func DecodePEXMessage(data []byte) (*PEXMessage, error) {
	var msg PEXMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("decode pex message: %w", err)
	}
	return &msg, nil
}

func (pm *PEXManager) evictOldest() {
	var oldestID string
	var oldestTime time.Time
	for id, p := range pm.peers {
		if oldestID == "" || p.LastSeen.Before(oldestTime) {
			oldestID = id
			oldestTime = p.LastSeen
		}
	}
	if oldestID != "" {
		delete(pm.peers, oldestID)
	}
}

func mergeAddresses(existing, new []string) []string {
	set := make(map[string]bool, len(existing)+len(new))
	for _, a := range existing {
		set[a] = true
	}
	for _, a := range new {
		set[a] = true
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
