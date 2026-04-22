package networking

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// EvidenceGossipIngress validates inbound consensus evidence gossip: JSON decode,
// deduplication, optional per-peer rate limits, and submission to EvidenceManager.
type EvidenceGossipIngress struct {
	em *chain.EvidenceManager
	rep *ReputationTracker

	mu          sync.Mutex
	seenIDs     map[string]time.Time
	maxSeen     int
	seenTTL     time.Duration
	window      time.Duration
	maxPerPeer  int
	peerBuckets map[string][]time.Time
}

// EvidenceGossipConfig limits memory and spam from evidence gossip.
type EvidenceGossipConfig struct {
	MaxSeenEntries int           // cap for dedupe map (oldest evicted)
	SeenEntryTTL   time.Duration // drop dedupe entries after this age
	RateWindow     time.Duration // sliding window for per-peer counts
	MaxPerWindow   int           // evidence messages per peer per window (0 = unlimited)
}

// DefaultEvidenceGossipConfig returns sensible defaults.
func DefaultEvidenceGossipConfig() EvidenceGossipConfig {
	return EvidenceGossipConfig{
		MaxSeenEntries: 100_000,
		SeenEntryTTL:   24 * time.Hour,
		RateWindow:     time.Minute,
		MaxPerWindow:   64,
	}
}

// NewEvidenceGossipIngress builds an evidence gossip handler.
func NewEvidenceGossipIngress(em *chain.EvidenceManager, rep *ReputationTracker, cfg EvidenceGossipConfig) *EvidenceGossipIngress {
	if cfg.MaxSeenEntries <= 0 {
		cfg.MaxSeenEntries = 100_000
	}
	if cfg.SeenEntryTTL <= 0 {
		cfg.SeenEntryTTL = 24 * time.Hour
	}
	if cfg.RateWindow <= 0 {
		cfg.RateWindow = time.Minute
	}
	if cfg.MaxPerWindow <= 0 {
		cfg.MaxPerWindow = 64
	}
	return &EvidenceGossipIngress{
		em:          em,
		rep:         rep,
		seenIDs:     make(map[string]time.Time),
		maxSeen:     cfg.MaxSeenEntries,
		seenTTL:     cfg.SeenEntryTTL,
		window:      cfg.RateWindow,
		maxPerPeer:  cfg.MaxPerWindow,
		peerBuckets: make(map[string][]time.Time),
	}
}

// HandlePeerMessage decodes evidence JSON, enforces dedupe and rate limits, then processes.
func (eg *EvidenceGossipIngress) HandlePeerMessage(peerID string, payload []byte) error {
	var ev chain.ConsensusEvidence
	if err := json.Unmarshal(payload, &ev); err != nil {
		if eg.rep != nil {
			eg.rep.RecordEvent(peerID, EventInvalidTx, 0)
		}
		return fmt.Errorf("evidence gossip decode: %w", err)
	}

	id := chain.StableEvidenceID(ev)
	now := time.Now()

	eg.mu.Lock()
	eg.pruneSeenLocked(now)
	if _, dup := eg.seenIDs[id]; dup {
		eg.mu.Unlock()
		return fmt.Errorf("duplicate evidence gossip: %s", id)
	}
	if !eg.allowPeerLocked(peerID, now) {
		eg.mu.Unlock()
		if eg.rep != nil {
			eg.rep.RecordEvent(peerID, EventProtocolViolation, 0)
		}
		return fmt.Errorf("evidence gossip rate limited for peer %s", peerID)
	}
	eg.seenIDs[id] = now
	eg.evictSeenIfNeededLocked()
	eg.mu.Unlock()

	_, err := eg.em.Process(ev)
	if err != nil {
		// Duplicate at manager layer (race) or validation failure
		if eg.rep != nil {
			eg.rep.RecordEvent(peerID, EventInvalidTx, 0)
		}
		return err
	}
	if eg.rep != nil {
		eg.rep.RecordEvent(peerID, EventValidBlock, 0)
	}
	return nil
}

func (eg *EvidenceGossipIngress) pruneSeenLocked(now time.Time) {
	if eg.seenTTL <= 0 {
		return
	}
	for k, ts := range eg.seenIDs {
		if now.Sub(ts) > eg.seenTTL {
			delete(eg.seenIDs, k)
		}
	}
	for peer, times := range eg.peerBuckets {
		var kept []time.Time
		for _, ts := range times {
			if now.Sub(ts) <= eg.window {
				kept = append(kept, ts)
			}
		}
		if len(kept) == 0 {
			delete(eg.peerBuckets, peer)
		} else {
			eg.peerBuckets[peer] = kept
		}
	}
}

func (eg *EvidenceGossipIngress) allowPeerLocked(peerID string, now time.Time) bool {
	times := eg.peerBuckets[peerID]
	var fresh []time.Time
	for _, ts := range times {
		if now.Sub(ts) <= eg.window {
			fresh = append(fresh, ts)
		}
	}
	if len(fresh) >= eg.maxPerPeer {
		return false
	}
	fresh = append(fresh, now)
	eg.peerBuckets[peerID] = fresh
	return true
}

func (eg *EvidenceGossipIngress) evictSeenIfNeededLocked() {
	if len(eg.seenIDs) <= eg.maxSeen {
		return
	}
	type kv struct {
		id string
		ts time.Time
	}
	list := make([]kv, 0, len(eg.seenIDs))
	for id, ts := range eg.seenIDs {
		list = append(list, kv{id, ts})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ts.Before(list[j].ts) })
	cut := len(list) / 10
	if cut < 1 {
		cut = 1
	}
	for i := 0; i < cut && i < len(list); i++ {
		delete(eg.seenIDs, list[i].id)
	}
}
