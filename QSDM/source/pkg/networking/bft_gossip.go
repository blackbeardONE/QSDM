package networking

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// BFTTopicName is the GossipSub topic for vote-driven BFT (propose / prevote / precommit).
const BFTTopicName = "qsdm-bft"

// BFTGossipConfig bounds memory and spam on the BFT topic.
type BFTGossipConfig struct {
	MaxSeenEntries int
	SeenEntryTTL   time.Duration
	RateWindow     time.Duration
	MaxPerWindow   int
}

// DefaultBFTGossipConfig returns conservative defaults.
func DefaultBFTGossipConfig() BFTGossipConfig {
	return BFTGossipConfig{
		MaxSeenEntries: 50_000,
		SeenEntryTTL:   24 * time.Hour,
		RateWindow:     time.Minute,
		MaxPerWindow:   256,
	}
}

// BFTGossipStats is a snapshot of ingress counters (for Prometheus / ops).
type BFTGossipStats struct {
	IngressOK     uint64
	DedupeDropped uint64
	RateLimited   uint64
	RejectedWire  uint64
	ApplyErrors   uint64
}

// BFTGossipIngress validates inbound BFT gossip (decode envelope, dedupe, rate limits) and applies to executor.
type BFTGossipIngress struct {
	exec *chain.BFTExecutor
	rep  *ReputationTracker
	// participates gates whether inbound consensus messages are applied
	// or only validated and relayed. See SetParticipationGate.
	participates func() bool

	mu          sync.Mutex
	seenIDs     map[string]time.Time
	maxSeen     int
	seenTTL     time.Duration
	window      time.Duration
	maxPerPeer  int
	peerBuckets map[string][]time.Time

	statIngressOK     atomic.Uint64
	statDedupe        atomic.Uint64
	statRateLimited   atomic.Uint64
	statRejectedWire  atomic.Uint64
	statApplyErrors   atomic.Uint64
}

// Stats returns ingress counters since process start.
func (g *BFTGossipIngress) Stats() BFTGossipStats {
	if g == nil {
		return BFTGossipStats{}
	}
	return BFTGossipStats{
		IngressOK:     g.statIngressOK.Load(),
		DedupeDropped: g.statDedupe.Load(),
		RateLimited:   g.statRateLimited.Load(),
		RejectedWire:  g.statRejectedWire.Load(),
		ApplyErrors:   g.statApplyErrors.Load(),
	}
}

// NewBFTGossipIngress builds a BFT gossip handler. exec may be nil (validate + dedupe only).
func NewBFTGossipIngress(cfg BFTGossipConfig, exec *chain.BFTExecutor) *BFTGossipIngress {
	if cfg.MaxSeenEntries <= 0 {
		cfg.MaxSeenEntries = 50_000
	}
	if cfg.SeenEntryTTL <= 0 {
		cfg.SeenEntryTTL = 24 * time.Hour
	}
	if cfg.RateWindow <= 0 {
		cfg.RateWindow = time.Minute
	}
	if cfg.MaxPerWindow <= 0 {
		cfg.MaxPerWindow = 256
	}
	return &BFTGossipIngress{
		exec:        exec,
		rep:         nil,
		seenIDs:     make(map[string]time.Time),
		maxSeen:     cfg.MaxSeenEntries,
		seenTTL:     cfg.SeenEntryTTL,
		window:      cfg.RateWindow,
		maxPerPeer:  cfg.MaxPerWindow,
		peerBuckets: make(map[string][]time.Time),
	}
}

// SetReputationTracker optionally penalizes peers who relay provable BFT equivocation payloads.
func (g *BFTGossipIngress) SetReputationTracker(rt *ReputationTracker) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rep = rt
}

// HandlePeerMessage validates a raw GossipSub payload and forwards it to the executor.
func (g *BFTGossipIngress) HandlePeerMessage(peerID string, payload []byte) error {
	// Refuse banned peers up front. This ingress already penalizes provable
	// equivocation via RecordEvent, but nothing consulted the resulting ban,
	// so an equivocating peer kept getting its consensus messages applied.
	g.mu.Lock()
	rep := g.rep
	g.mu.Unlock()
	if rep != nil && rep.IsBanned(peerID) {
		g.statRejectedWire.Add(1)
		return fmt.Errorf("bft gossip refused: peer %s is banned", peerID)
	}

	var env chain.BFTWireEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		g.statRejectedWire.Add(1)
		return fmt.Errorf("bft gossip decode: %w", err)
	}
	if env.Kind == "" || len(env.Payload) == 0 {
		g.statRejectedWire.Add(1)
		return fmt.Errorf("bft gossip empty kind or payload")
	}
	switch env.Kind {
	case chain.BFTWirePropose, chain.BFTWirePrevote, chain.BFTWirePrecommit:
	default:
		g.statRejectedWire.Add(1)
		return fmt.Errorf("bft gossip unknown kind %q", env.Kind)
	}

	sum := sha256.Sum256(payload)
	id := env.Kind + ":" + hex.EncodeToString(sum[:])
	now := time.Now()

	g.mu.Lock()
	g.pruneSeenLocked(now)
	if _, dup := g.seenIDs[id]; dup {
		g.mu.Unlock()
		g.statDedupe.Add(1)
		return fmt.Errorf("duplicate bft gossip")
	}
	if !g.allowPeerLocked(peerID, now) {
		g.mu.Unlock()
		g.statRateLimited.Add(1)
		return fmt.Errorf("bft gossip rate limited for peer %s", peerID)
	}
	g.seenIDs[id] = now
	g.evictSeenIfNeededLocked()
	g.mu.Unlock()

	// Apply only when this node is a participating validator. A replica
	// still validated, deduped and relayed the message above, which is the
	// useful work it can do for the network without being in the set.
	//
	// Previously this was decided once at construction by nil-ing the
	// executor whenever QSDM_NETWORKED_CATCHUP_MODE was set, so a node that
	// later bonded stake and joined the validator set kept behaving as a
	// replica until an operator restarted it.
	if g.exec != nil && g.participating() {
		g.exec.SetLastInboundBFTGossipPeer(peerID)
		if err := g.exec.ApplyInbound(payload); err != nil {
			g.statApplyErrors.Add(1)
			g.mu.Lock()
			rep := g.rep
			g.mu.Unlock()
			if rep != nil && errors.Is(err, chain.ErrBFTEquivocation) {
				rep.RecordEvent(peerID, EventProtocolViolation, 0)
			}
			return err
		}
	}
	g.statIngressOK.Add(1)
	return nil
}

func (g *BFTGossipIngress) pruneSeenLocked(now time.Time) {
	cutoff := now.Add(-g.seenTTL)
	for id, t := range g.seenIDs {
		if t.Before(cutoff) {
			delete(g.seenIDs, id)
		}
	}
	winStart := now.Add(-g.window)
	for peer, times := range g.peerBuckets {
		kept := times[:0]
		for _, ts := range times {
			if !ts.Before(winStart) {
				kept = append(kept, ts)
			}
		}
		if len(kept) == 0 {
			delete(g.peerBuckets, peer)
		} else {
			g.peerBuckets[peer] = kept
		}
	}
}

func (g *BFTGossipIngress) allowPeerLocked(peerID string, now time.Time) bool {
	if g.maxPerPeer <= 0 {
		return true
	}
	winStart := now.Add(-g.window)
	bucket := g.peerBuckets[peerID]
	n := 0
	for _, ts := range bucket {
		if !ts.Before(winStart) {
			n++
		}
	}
	if n >= g.maxPerPeer {
		return false
	}
	g.peerBuckets[peerID] = append(bucket, now)
	return true
}

func (g *BFTGossipIngress) evictSeenIfNeededLocked() {
	for len(g.seenIDs) > g.maxSeen {
		var oldestID string
		var oldest time.Time
		first := true
		for id, t := range g.seenIDs {
			if first || t.Before(oldest) {
				first = false
				oldest = t
				oldestID = id
			}
		}
		if oldestID == "" {
			return
		}
		delete(g.seenIDs, oldestID)
	}
}

// SetParticipationGate installs a predicate deciding whether inbound BFT
// consensus messages are APPLIED (true) or merely validated and relayed
// (false).
//
// This replaces nil-ing the executor at construction, which cmd/qsdm did
// whenever QSDM_NETWORKED_CATCHUP_MODE was set. That was a static, boot-time
// decision: a node started as a catch-up replica stayed one for the life of
// the process, even after it bonded stake and legitimately joined the
// validator set. Since validator membership is now derived from committed
// chain state and reconciled on every commit
// (pkg/chain/validator_registry_chainstate.go), participation has to be able
// to follow it without a restart.
//
// A nil gate means "apply", preserving the behaviour of a full validator.
// Wire-level validation, dedupe and relay run regardless of the gate, so a
// non-participating node still helps propagate consensus traffic — which is
// what makes a home replica useful to the network even before it bonds.
func (g *BFTGossipIngress) SetParticipationGate(fn func() bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.participates = fn
}

// participating reports whether inbound consensus messages should be applied.
func (g *BFTGossipIngress) participating() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	fn := g.participates
	g.mu.Unlock()
	if fn == nil {
		return true
	}
	return fn()
}
