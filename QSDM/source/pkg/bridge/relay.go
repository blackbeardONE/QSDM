package bridge

import (
	"context"
	"encoding/json"
	"log"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

const BridgeTopicName = "qsdm-bridge"

// P2PRelay broadcasts and receives bridge events over libp2p pubsub.
type P2PRelay struct {
	topic  *pubsub.Topic
	sub    *pubsub.Subscription
	bridge *BridgeProtocol
	swap   *AtomicSwapProtocol
	ctx    context.Context
	cancel context.CancelFunc
}

// BridgeP2PEvent is the envelope sent/received over the wire.
type BridgeP2PEvent struct {
	Kind       string          `json:"kind"` // "lock_created","lock_redeemed","lock_refunded","swap_initiated","swap_participated","swap_completed","swap_refunded"
	Payload    json.RawMessage `json:"payload"`
	OriginNode string          `json:"origin_node"`
	Timestamp  string          `json:"ts"`
}

type TopicJoiner interface {
	JoinTopic(name string) (*pubsub.Topic, *pubsub.Subscription, error)
}

// NewP2PRelay creates a relay that publishes bridge events to the P2P network
// and applies incoming events from other nodes.
func NewP2PRelay(net TopicJoiner, bp *BridgeProtocol, asp *AtomicSwapProtocol, nodeID string) (*P2PRelay, error) {
	t, s, err := net.JoinTopic(BridgeTopicName)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := &P2PRelay{
		topic:  t,
		sub:    s,
		bridge: bp,
		swap:   asp,
		ctx:    ctx,
		cancel: cancel,
	}

	go r.readLoop(nodeID)
	return r, nil
}

func (r *P2PRelay) readLoop(selfNodeID string) {
	for {
		msg, err := r.sub.Next(r.ctx)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			log.Printf("[bridge-relay] pubsub read error: %v", err)
			continue
		}

		var evt BridgeP2PEvent
		if err := json.Unmarshal(msg.Data, &evt); err != nil {
			log.Printf("[bridge-relay] malformed event: %v", err)
			continue
		}

		if evt.OriginNode == selfNodeID {
			continue
		}

		r.handleRemoteEvent(evt)
	}
}

func (r *P2PRelay) handleRemoteEvent(evt BridgeP2PEvent) {
	switch evt.Kind {
	case "lock_created":
		var lk Lock
		if err := json.Unmarshal(evt.Payload, &lk); err != nil {
			return
		}
		if r.bridge != nil {
			r.bridge.mu.Lock()
			if _, exists := r.bridge.locks[lk.ID]; !exists {
				r.bridge.locks[lk.ID] = &lk
			}
			r.bridge.mu.Unlock()
		}
		log.Printf("[bridge-relay] applied remote lock %s from %s", lk.ID, evt.OriginNode)

	case "lock_redeemed":
		var info struct {
			LockID string `json:"lock_id"`
		}
		if err := json.Unmarshal(evt.Payload, &info); err != nil {
			return
		}
		if r.bridge != nil {
			r.bridge.mu.Lock()
			if l, ok := r.bridge.locks[info.LockID]; ok && l.Status == LockStatusLocked {
				l.Status = LockStatusRedeemed
			}
			r.bridge.mu.Unlock()
		}

	case "lock_refunded":
		var info struct {
			LockID string `json:"lock_id"`
		}
		if err := json.Unmarshal(evt.Payload, &info); err != nil {
			return
		}
		if r.bridge != nil {
			r.bridge.mu.Lock()
			if l, ok := r.bridge.locks[info.LockID]; ok {
				l.Status = LockStatusRefunded
			}
			r.bridge.mu.Unlock()
		}

	case "swap_initiated":
		var sw Swap
		if err := json.Unmarshal(evt.Payload, &sw); err != nil {
			return
		}
		// Do NOT propagate raw secrets — only the hash travels.
		sw.InitiatorSecret = ""
		sw.ParticipantSecret = ""
		if r.swap != nil {
			r.swap.mu.Lock()
			if _, exists := r.swap.swaps[sw.ID]; !exists {
				r.swap.swaps[sw.ID] = &sw
			}
			r.swap.mu.Unlock()
		}

	case "swap_completed":
		var info struct {
			SwapID string `json:"swap_id"`
		}
		if err := json.Unmarshal(evt.Payload, &info); err != nil {
			return
		}
		if r.swap != nil {
			r.swap.mu.Lock()
			if s, ok := r.swap.swaps[info.SwapID]; ok {
				s.Status = SwapStatusCompleted
			}
			r.swap.mu.Unlock()
		}
	}
}

// PublishLockEvent broadcasts a lock event to peer nodes.
// The lock's raw Secret field is stripped before publishing.
func (r *P2PRelay) PublishLockEvent(kind string, lock *Lock, nodeID string) {
	if lock == nil {
		return
	}
	safe := *lock
	safe.Secret = "" // never broadcast the HTLC pre-image
	payload, _ := json.Marshal(safe)
	r.publish(kind, payload, nodeID)
}

// PublishSwapEvent broadcasts a swap event to peer nodes.
// Raw secrets are stripped before publishing.
func (r *P2PRelay) PublishSwapEvent(kind string, swap *Swap, nodeID string) {
	if swap == nil {
		return
	}
	safe := *swap
	safe.InitiatorSecret = ""
	safe.ParticipantSecret = ""
	payload, _ := json.Marshal(safe)
	r.publish(kind, payload, nodeID)
}

func (r *P2PRelay) publish(kind string, payload json.RawMessage, nodeID string) {
	evt := BridgeP2PEvent{
		Kind:       kind,
		Payload:    payload,
		OriginNode: nodeID,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	if err := r.topic.Publish(r.ctx, data); err != nil {
		log.Printf("[bridge-relay] publish %s failed: %v", kind, err)
	}
}

// Close stops the relay goroutine.
func (r *P2PRelay) Close() {
	r.cancel()
}
