package networking

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// TxGossipRelayConfig limits outbound gossip fan-out.
type TxGossipRelayConfig struct {
	MaxPublishPerSecond float64
	SeenTTL             time.Duration
	MaxSeenKeys         int
}

// DefaultTxGossipRelayConfig returns conservative relay defaults.
func DefaultTxGossipRelayConfig() TxGossipRelayConfig {
	return TxGossipRelayConfig{
		MaxPublishPerSecond: 80,
		SeenTTL:             5 * time.Minute,
		MaxSeenKeys:         100_000,
	}
}

// TxGossipRelay deduplicates and rate-limits re-publishing accepted signed tx gossip.
type TxGossipRelay struct {
	mu       sync.Mutex
	publish  func([]byte) error
	cfg      TxGossipRelayConfig
	seen     map[string]time.Time
	tokens   float64
	lastFill time.Time
}

// NewTxGossipRelay builds a relay. publish is typically net.Broadcast.
func NewTxGossipRelay(publish func([]byte) error, cfg TxGossipRelayConfig) *TxGossipRelay {
	if publish == nil {
		return nil
	}
	if cfg.MaxPublishPerSecond <= 0 {
		cfg.MaxPublishPerSecond = 80
	}
	if cfg.SeenTTL <= 0 {
		cfg.SeenTTL = 5 * time.Minute
	}
	if cfg.MaxSeenKeys <= 0 {
		cfg.MaxSeenKeys = 100_000
	}
	return &TxGossipRelay{
		publish:  publish,
		cfg:      cfg,
		seen:     make(map[string]time.Time),
		tokens:   cfg.MaxPublishPerSecond,
		lastFill: time.Now(),
	}
}

func (r *TxGossipRelay) prune(now time.Time) {
	cutoff := now.Add(-r.cfg.SeenTTL)
	for id, t := range r.seen {
		if t.Before(cutoff) {
			delete(r.seen, id)
		}
	}
	for len(r.seen) > r.cfg.MaxSeenKeys {
		for id := range r.seen {
			delete(r.seen, id)
			break
		}
	}
}

func (r *TxGossipRelay) takeToken(now time.Time) bool {
	dt := now.Sub(r.lastFill).Seconds()
	if dt > 0 {
		r.tokens += dt * r.cfg.MaxPublishPerSecond
		if r.tokens > r.cfg.MaxPublishPerSecond {
			r.tokens = r.cfg.MaxPublishPerSecond
		}
		r.lastFill = now
	}
	if r.tokens >= 1 {
		r.tokens--
		return true
	}
	return false
}

// MaybePublish broadcasts payload once per txID subject to dedupe and rate limits.
func (r *TxGossipRelay) MaybePublish(txID string, payload []byte) error {
	if r == nil || txID == "" || len(payload) == 0 {
		return nil
	}
	now := time.Now()
	r.mu.Lock()
	r.prune(now)
	if _, dup := r.seen[txID]; dup {
		r.mu.Unlock()
		return nil
	}
	if !r.takeToken(now) {
		r.mu.Unlock()
		return nil
	}
	body := append([]byte(nil), payload...)
	r.seen[txID] = now
	r.mu.Unlock()
	err := r.publish(body)
	if err != nil {
		r.mu.Lock()
		delete(r.seen, txID)
		r.tokens++
		r.mu.Unlock()
	}
	return err
}

// MaybePublishOpaque applies dedupe + rate limits to an arbitrary payload (e.g. wallet bytes),
// using a stable id derived from the payload hash.
func (r *TxGossipRelay) MaybePublishOpaque(payload []byte) error {
	if r == nil || len(payload) == 0 {
		return nil
	}
	sum := sha256.Sum256(payload)
	id := "opaque:" + hex.EncodeToString(sum[:])
	return r.MaybePublish(id, payload)
}
