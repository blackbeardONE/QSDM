package networking

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// gossipGuard is the GossipSub default validator installed on every topic.
// It runs inline (before any topic handler decodes JSON or verifies a
// signature) and enforces two cheap checks on messages received from OTHER
// peers:
//
//   - per-topic payload ceiling: an oversized payload is REJECTED, which also
//     feeds GossipSub peer scoring (P4 invalid deliveries) when scoring is on;
//   - per-(topic, forwarding peer) token bucket: excess messages are IGNORED
//     (dropped, not forwarded, no score penalty), because the forwarding peer
//     may be an honest relay of someone else's flood.
//
// Messages published by this node (pid == self) are always accepted, so a
// node's own publications -- in particular the single-node producer's blocks
// and BFT messages -- are untouched by these limits.
type gossipGuard struct {
	self      peer.ID
	topicMax  map[string]int
	globalMax int
	rate      float64 // tokens / second; <= 0 disables
	burst     float64

	mu      sync.Mutex
	buckets map[guardKey]*guardBucket
	lastGC  time.Time
	now     func() time.Time

	statOversize    atomic.Uint64
	statRateLimited atomic.Uint64
	statAccepted    atomic.Uint64
}

type guardKey struct {
	topic string
	peer  peer.ID
}

type guardBucket struct {
	tokens float64
	last   time.Time
}

// GossipGuardStats is a snapshot of the gossip guard counters.
type GossipGuardStats struct {
	Accepted         uint64
	RejectedOversize uint64
	RateLimited      uint64
}

const gossipGuardIdleTTL = 10 * time.Minute

func newGossipGuard(self peer.ID, l P2PLimits) *gossipGuard {
	g := &gossipGuard{
		self:      self,
		topicMax:  make(map[string]int, len(l.GossipTopicMaxBytes)),
		globalMax: l.GossipMaxMessageBytes,
		rate:      l.GossipPeerRate,
		burst:     float64(l.GossipPeerBurst),
		buckets:   make(map[guardKey]*guardBucket),
		now:       time.Now,
	}
	for t, n := range l.GossipTopicMaxBytes {
		g.topicMax[t] = n
	}
	if g.burst < 1 {
		g.burst = 1
	}
	return g
}

// Stats returns the guard counters since start.
func (g *gossipGuard) Stats() GossipGuardStats {
	if g == nil {
		return GossipGuardStats{}
	}
	return GossipGuardStats{
		Accepted:         g.statAccepted.Load(),
		RejectedOversize: g.statOversize.Load(),
		RateLimited:      g.statRateLimited.Load(),
	}
}

func (g *gossipGuard) validate(_ context.Context, pid peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
	if pid == g.self || msg == nil {
		return pubsub.ValidationAccept
	}
	topic := msg.GetTopic()
	if !g.sizeOK(topic, len(msg.Data)) {
		g.statOversize.Add(1)
		return pubsub.ValidationReject
	}
	if !g.allow(topic, pid) {
		g.statRateLimited.Add(1)
		return pubsub.ValidationIgnore
	}
	g.statAccepted.Add(1)
	return pubsub.ValidationAccept
}

func (g *gossipGuard) sizeOK(topic string, n int) bool {
	max, ok := g.topicMax[topic]
	if !ok || max <= 0 {
		max = g.globalMax
	}
	return max <= 0 || n <= max
}

func (g *gossipGuard) allow(topic string, pid peer.ID) bool {
	if g.rate <= 0 {
		return true
	}
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Sub(g.lastGC) > gossipGuardIdleTTL {
		for k, b := range g.buckets {
			if now.Sub(b.last) > gossipGuardIdleTTL {
				delete(g.buckets, k)
			}
		}
		g.lastGC = now
	}
	k := guardKey{topic: topic, peer: pid}
	b := g.buckets[k]
	if b == nil {
		b = &guardBucket{tokens: g.burst, last: now}
		g.buckets[k] = b
	} else if dt := now.Sub(b.last).Seconds(); dt > 0 {
		b.tokens += dt * g.rate
		if b.tokens > g.burst {
			b.tokens = g.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
