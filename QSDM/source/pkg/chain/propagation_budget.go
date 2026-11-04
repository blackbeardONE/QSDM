package chain

import (
	"sync"
	"time"
)

// Block-request response budget (QSDM/docs/docs/P2P_LIMITS.md, risk 2).
//
// One small block_request makes every node with a block provider publish a
// response of up to 64 blocks (at most 1 MiB, the topic's message limit) to
// the whole topic. The gossip guard caps each neighbour at 100 messages/s,
// which still lets one abusive peer pull on the order of 100 MiB/s of
// responses out of a node. So a node answers at most:
//   - one request per requesting peer every 2 s (burst 4). The key is the
//     pubsub author, which StrictSign authenticates; the envelope's
//     OriginNode is self-declared and is not used;
//   - two requests per second in total (burst 8), which bounds what many
//     peers, or many identities, can pull together.
//
// An honest node asks once per 10 s catch-up tick, so these limits never slow
// a legitimate catch-up. A request over budget gets no reply; the requester
// asks again on its next tick, or uses HTTP sync.
const (
	blockResponsePeerEvery   = 2 * time.Second
	blockResponsePeerBurst   = 4
	blockResponseGlobalEvery = 500 * time.Millisecond
	blockResponseGlobalBurst = 8
	// blockResponseMaxPeers bounds the per-peer table. When it is full,
	// buckets idle long enough to be full again are swept (at most once a
	// second); if none can go, a peer not in the table is refused.
	blockResponseMaxPeers = 4096
)

// tokenBucket earns one token per `every`, up to `burst`.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

// refill adds the tokens earned since the last call. A bucket that has never
// been used starts full. A clock that steps backwards earns nothing.
func (b *tokenBucket) refill(now time.Time, every time.Duration, burst float64) {
	if b.last.IsZero() {
		b.tokens, b.last = burst, now
		return
	}
	if now.After(b.last) {
		b.tokens += float64(now.Sub(b.last)) / float64(every)
		if b.tokens > burst {
			b.tokens = burst
		}
		b.last = now
	}
}

type responseBudget struct {
	peerEvery, globalEvery time.Duration
	peerBurst, globalBurst float64
	maxPeers               int
	now                    func() time.Time // nil: time.Now

	mu        sync.Mutex
	global    tokenBucket
	peers     map[string]*tokenBucket
	lastSweep time.Time
	dropped   uint64
}

func newResponseBudget() *responseBudget {
	return &responseBudget{
		peerEvery:   blockResponsePeerEvery,
		peerBurst:   blockResponsePeerBurst,
		globalEvery: blockResponseGlobalEvery,
		globalBurst: blockResponseGlobalBurst,
		maxPeers:    blockResponseMaxPeers,
		peers:       make(map[string]*tokenBucket),
	}
}

// allow reports whether a response to a request from peer fits the budget. If
// it does, one token is spent from the peer's bucket and one from the global
// bucket. A refused request spends nothing.
func (rb *responseBudget) allow(peer string) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	now := time.Now()
	if rb.now != nil {
		now = rb.now()
	}
	rb.global.refill(now, rb.globalEvery, rb.globalBurst)
	if rb.global.tokens < 1 {
		rb.dropped++
		return false
	}
	pb := rb.peers[peer]
	if pb == nil {
		if len(rb.peers) >= rb.maxPeers {
			rb.sweep(now)
		}
		if len(rb.peers) >= rb.maxPeers {
			rb.dropped++
			return false
		}
		pb = &tokenBucket{}
		rb.peers[peer] = pb
	}
	pb.refill(now, rb.peerEvery, rb.peerBurst)
	if pb.tokens < 1 {
		rb.dropped++
		return false
	}
	pb.tokens--
	rb.global.tokens--
	return true
}

// sweep forgets peers whose bucket has had time to fill up again: a new
// bucket for them would start full anyway. It runs at most once a second.
func (rb *responseBudget) sweep(now time.Time) {
	if !rb.lastSweep.IsZero() && now.Sub(rb.lastSweep) < time.Second {
		return
	}
	rb.lastSweep = now
	idle := time.Duration(rb.peerBurst * float64(rb.peerEvery))
	for k, b := range rb.peers {
		if now.Sub(b.last) >= idle {
			delete(rb.peers, k)
		}
	}
}

func (rb *responseBudget) droppedCount() uint64 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.dropped
}
