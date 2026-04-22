package networking

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

const (
	polKindPrevoteLock      = "prevote_lock"
	polKindRoundCertificate = "round_certificate"
)

// polGossipWire is the GossipSub envelope for prevote-lock proofs and round certificates.
type polGossipWire struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// PolGossipConfig bounds memory and spam on the POL topic.
type PolGossipConfig struct {
	MaxSeenEntries int
	SeenEntryTTL   time.Duration
	RateWindow     time.Duration
	MaxPerWindow   int
}

// DefaultPolGossipConfig returns conservative defaults.
func DefaultPolGossipConfig() PolGossipConfig {
	return PolGossipConfig{
		MaxSeenEntries: 50_000,
		SeenEntryTTL:   24 * time.Hour,
		RateWindow:     time.Minute,
		MaxPerWindow:   128,
	}
}

// PolGossipIngress validates inbound POL / round-certificate gossip (decode, dedupe, rate limits).
type PolGossipIngress struct {
	follower *chain.PolFollower
	mu          sync.Mutex
	seenIDs     map[string]time.Time
	maxSeen     int
	seenTTL     time.Duration
	window      time.Duration
	maxPerPeer  int
	peerBuckets map[string][]time.Time
}

// NewPolGossipIngress builds a POL gossip handler. follower may be nil (receive-only validate + dedupe).
func NewPolGossipIngress(cfg PolGossipConfig, follower *chain.PolFollower) *PolGossipIngress {
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
		cfg.MaxPerWindow = 128
	}
	return &PolGossipIngress{
		follower:    follower,
		seenIDs:     make(map[string]time.Time),
		maxSeen:     cfg.MaxSeenEntries,
		seenTTL:     cfg.SeenEntryTTL,
		window:      cfg.RateWindow,
		maxPerPeer:  cfg.MaxPerWindow,
		peerBuckets: make(map[string][]time.Time),
	}
}

// HandlePeerMessage decodes a POL gossip envelope, enforces dedupe and rate limits.
func (p *PolGossipIngress) HandlePeerMessage(peerID string, payload []byte) error {
	var wire polGossipWire
	if err := json.Unmarshal(payload, &wire); err != nil {
		return fmt.Errorf("pol gossip decode: %w", err)
	}
	if len(wire.Payload) == 0 {
		return fmt.Errorf("pol gossip empty payload")
	}
	switch wire.Kind {
	case polKindPrevoteLock:
		pl, err := chain.DecodePrevoteLockProof(wire.Payload)
		if err != nil || pl == nil {
			return fmt.Errorf("pol gossip prevote_lock payload: %w", err)
		}
		if pl.Height == 0 || pl.LockedBlockHash == "" {
			return fmt.Errorf("pol gossip prevote_lock invalid fields")
		}
		if p.follower != nil {
			if err := p.follower.IngestPrevoteLockProof(pl); err != nil {
				return err
			}
		}
	case polKindRoundCertificate:
		var cert chain.RoundCertificate
		if err := json.Unmarshal(wire.Payload, &cert); err != nil {
			return fmt.Errorf("pol gossip round_certificate payload: %w", err)
		}
		if cert.Height == 0 || cert.CommitDigest == "" {
			return fmt.Errorf("pol gossip round_certificate invalid fields")
		}
		if p.follower != nil {
			if err := p.follower.IngestRoundCertificate(&cert); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("pol gossip unknown kind %q", wire.Kind)
	}

	sum := sha256.Sum256(payload)
	id := wire.Kind + ":" + hex.EncodeToString(sum[:])
	now := time.Now()

	p.mu.Lock()
	p.pruneSeenLocked(now)
	if _, dup := p.seenIDs[id]; dup {
		p.mu.Unlock()
		return fmt.Errorf("duplicate pol gossip")
	}
	if !p.allowPeerLocked(peerID, now) {
		p.mu.Unlock()
		return fmt.Errorf("pol gossip rate limited for peer %s", peerID)
	}
	p.seenIDs[id] = now
	p.evictSeenIfNeededLocked()
	p.mu.Unlock()
	return nil
}

func (p *PolGossipIngress) pruneSeenLocked(now time.Time) {
	cutoff := now.Add(-p.seenTTL)
	for id, t := range p.seenIDs {
		if t.Before(cutoff) {
			delete(p.seenIDs, id)
		}
	}
	winStart := now.Add(-p.window)
	for peer, times := range p.peerBuckets {
		kept := times[:0]
		for _, ts := range times {
			if !ts.Before(winStart) {
				kept = append(kept, ts)
			}
		}
		if len(kept) == 0 {
			delete(p.peerBuckets, peer)
		} else {
			p.peerBuckets[peer] = kept
		}
	}
}

func (p *PolGossipIngress) allowPeerLocked(peerID string, now time.Time) bool {
	if p.maxPerPeer <= 0 {
		return true
	}
	winStart := now.Add(-p.window)
	bucket := p.peerBuckets[peerID]
	n := 0
	for _, ts := range bucket {
		if !ts.Before(winStart) {
			n++
		}
	}
	if n >= p.maxPerPeer {
		return false
	}
	p.peerBuckets[peerID] = append(bucket, now)
	return true
}

func (p *PolGossipIngress) evictSeenIfNeededLocked() {
	for len(p.seenIDs) > p.maxSeen {
		var oldestID string
		var oldest time.Time
		first := true
		for id, t := range p.seenIDs {
			if first || t.Before(oldest) {
				first = false
				oldest = t
				oldestID = id
			}
		}
		if oldestID == "" {
			return
		}
		delete(p.seenIDs, oldestID)
	}
}
