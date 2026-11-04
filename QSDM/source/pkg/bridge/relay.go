package bridge

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

const BridgeTopicName = "qsdm-bridge"

// P2PRelay broadcasts and receives bridge events over libp2p pubsub.
//
// Remote events are applied only when they carry a valid ML-DSA-87
// signature from a trusted bridge-relayer key (see relay_auth.go). A relay
// without trusted keys still publishes local events but never applies
// remote ones.
type P2PRelay struct {
	topic  *pubsub.Topic
	sub    *pubsub.Subscription
	bridge *BridgeProtocol
	swap   *AtomicSwapProtocol
	ctx    context.Context
	cancel context.CancelFunc

	auth          *relayAuth
	stats         relayCounters
	warnNoKeyOnce sync.Once
}

// BridgeP2PEvent is the envelope sent/received over the wire.
type BridgeP2PEvent struct {
	Kind       string          `json:"kind"` // "lock_created","lock_redeemed","lock_refunded","swap_initiated","swap_participated","swap_completed","swap_refunded"
	Payload    json.RawMessage `json:"payload"`
	OriginNode string          `json:"origin_node"`
	Timestamp  string          `json:"ts"`
	// SignerKeyID is hex(sha256(ML-DSA-87 public key)) of the relayer key
	// that signed the event; Signature is base64 of the ML-DSA-87 signature
	// over bridgeEventSigningBytes. Both are empty on unsigned events.
	SignerKeyID string `json:"signer_key_id,omitempty"`
	Signature   string `json:"sig,omitempty"`
}

type TopicJoiner interface {
	JoinTopic(name string) (*pubsub.Topic, *pubsub.Subscription, error)
}

// NewP2PRelay creates a relay with no trusted relayer keys and no signer:
// local events are published unsigned and remote events are never applied.
func NewP2PRelay(net TopicJoiner, bp *BridgeProtocol, asp *AtomicSwapProtocol, nodeID string) (*P2PRelay, error) {
	return NewP2PRelayWithAuth(net, bp, asp, nodeID, RelayAuthConfig{})
}

// NewP2PRelayWithAuth creates a relay that publishes bridge events to the
// P2P network (signed when auth.Signer is set) and applies incoming events
// that are signed by a key in auth.TrustedKeys.
func NewP2PRelayWithAuth(net TopicJoiner, bp *BridgeProtocol, asp *AtomicSwapProtocol, nodeID string, auth RelayAuthConfig) (*P2PRelay, error) {
	a, err := newRelayAuth(auth)
	if err != nil {
		return nil, err
	}
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
		auth:   a,
	}

	go r.readLoop(nodeID)
	return r, nil
}

// Stats returns counters for events received from remote peers.
func (r *P2PRelay) Stats() RelayStats {
	if r == nil {
		return RelayStats{}
	}
	return r.stats.snapshot()
}

// TrustedKeyCount reports how many relayer keys this relay accepts.
func (r *P2PRelay) TrustedKeyCount() int {
	if r == nil || r.auth == nil {
		return 0
	}
	return len(r.auth.trusted)
}

// Signing reports whether locally published events are signed.
func (r *P2PRelay) Signing() bool {
	return r != nil && r.auth != nil && r.auth.signer != nil
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
		if msg.ReceivedFrom.String() == selfNodeID {
			continue // our own publish delivered to our own subscription
		}
		r.processRemote(msg.Data, selfNodeID)
	}
}

// processRemote decodes, authenticates and applies one event received from
// a remote peer. Rejections are counted, not logged per event, so a flood
// cannot fill the log.
func (r *P2PRelay) processRemote(data []byte, selfNodeID string) {
	var evt BridgeP2PEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		r.stats.malformed.Add(1)
		return
	}
	if evt.OriginNode == selfNodeID {
		return
	}
	if err := r.auth.verify(&evt); err != nil {
		r.stats.countAuthErr(err)
		if err == errNoTrustedKeys {
			r.warnNoKeyOnce.Do(func() {
				log.Printf("[bridge-relay] ignoring remote bridge events: no trusted bridge relayer keys configured (see relay stats)")
			})
		}
		return
	}
	switch r.handleRemoteEvent(evt) {
	case remoteApplied:
		r.stats.applied.Add(1)
		log.Printf("[bridge-relay] applied %s from %s (signer %.16s)", evt.Kind, evt.OriginNode, evt.SignerKeyID)
	case remoteNoOp:
		r.stats.noop.Add(1)
	case remoteRejected:
		r.stats.badTransition.Add(1)
	}
}

type remoteOutcome int

const (
	remoteNoOp remoteOutcome = iota
	remoteApplied
	remoteRejected
)

// objectRef decodes the ID of an existing lock or swap from a status event.
// Publishers send the full object (key "ID"); the older wire form used
// "lock_id" / "swap_id". Both are accepted.
type objectRef struct {
	LockID string `json:"lock_id"`
	SwapID string `json:"swap_id"`
	ID     string `json:"ID"`
}

func decodeRef(payload json.RawMessage, lock bool) (string, bool) {
	var ref objectRef
	if err := json.Unmarshal(payload, &ref); err != nil {
		return "", false
	}
	id := ref.ID
	if lock && ref.LockID != "" {
		id = ref.LockID
	}
	if !lock && ref.SwapID != "" {
		id = ref.SwapID
	}
	return id, validBridgeObjectID(id)
}

// handleRemoteEvent applies an already authenticated event. Every change is
// checked against local state: objects are only created in their initial
// state, and status events only move an existing object along a legal
// transition. Terminal states are never created or overwritten.
func (r *P2PRelay) handleRemoteEvent(evt BridgeP2PEvent) remoteOutcome {
	switch evt.Kind {
	case "lock_created":
		if r.bridge == nil {
			return remoteNoOp
		}
		var lk Lock
		if err := json.Unmarshal(evt.Payload, &lk); err != nil {
			return remoteRejected
		}
		if !validBridgeObjectID(lk.ID) || lk.Status != LockStatusLocked {
			return remoteRejected
		}
		lk.Secret = "" // never accept or store a pre-image from gossip
		r.bridge.mu.Lock()
		defer r.bridge.mu.Unlock()
		if _, exists := r.bridge.locks[lk.ID]; exists {
			return remoteNoOp
		}
		r.bridge.locks[lk.ID] = &lk
		return remoteApplied

	case "lock_redeemed", "lock_refunded":
		if r.bridge == nil {
			return remoteNoOp
		}
		id, ok := decodeRef(evt.Payload, true)
		if !ok {
			return remoteRejected
		}
		target := LockStatusRedeemed
		if evt.Kind == "lock_refunded" {
			target = LockStatusRefunded
		}
		r.bridge.mu.Lock()
		defer r.bridge.mu.Unlock()
		l, exists := r.bridge.locks[id]
		if !exists {
			return remoteRejected // no terminal state for an unknown lock
		}
		if l.Status == target {
			return remoteNoOp
		}
		if l.Status != LockStatusLocked {
			return remoteRejected
		}
		l.Status = target
		return remoteApplied

	case "swap_initiated":
		if r.swap == nil {
			return remoteNoOp
		}
		var sw Swap
		if err := json.Unmarshal(evt.Payload, &sw); err != nil {
			return remoteRejected
		}
		if !validBridgeObjectID(sw.ID) || sw.Status != SwapStatusInitiated {
			return remoteRejected
		}
		// Do NOT accept raw secrets — only the hash travels.
		sw.InitiatorSecret = ""
		sw.ParticipantSecret = ""
		r.swap.mu.Lock()
		defer r.swap.mu.Unlock()
		if _, exists := r.swap.swaps[sw.ID]; exists {
			return remoteNoOp
		}
		r.swap.swaps[sw.ID] = &sw
		return remoteApplied

	case "swap_participated", "swap_completed":
		if r.swap == nil {
			return remoteNoOp
		}
		id, ok := decodeRef(evt.Payload, false)
		if !ok {
			return remoteRejected
		}
		r.swap.mu.Lock()
		defer r.swap.mu.Unlock()
		s, exists := r.swap.swaps[id]
		if !exists {
			return remoteRejected
		}
		if evt.Kind == "swap_participated" {
			switch s.Status {
			case SwapStatusParticipated, SwapStatusCompleted:
				return remoteNoOp
			case SwapStatusInitiated:
				s.Status = SwapStatusParticipated
				return remoteApplied
			}
			return remoteRejected
		}
		switch s.Status {
		case SwapStatusCompleted:
			return remoteNoOp
		case SwapStatusInitiated, SwapStatusParticipated:
			// Initiated is accepted because swap_participated may be
			// missed or arrive after swap_completed on gossip.
			s.Status = SwapStatusCompleted
			return remoteApplied
		}
		return remoteRejected
	}
	// swap_refunded and unknown kinds are not applied from gossip.
	return remoteNoOp
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

// buildEvent builds the wire envelope, signed when a relayer key is set.
func (r *P2PRelay) buildEvent(kind string, payload json.RawMessage, nodeID string) (BridgeP2PEvent, error) {
	evt := BridgeP2PEvent{
		Kind:       kind,
		Payload:    payload,
		OriginNode: nodeID,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	err := r.auth.sign(&evt)
	return evt, err
}

func (r *P2PRelay) publish(kind string, payload json.RawMessage, nodeID string) {
	evt, err := r.buildEvent(kind, payload, nodeID)
	if err != nil {
		log.Printf("[bridge-relay] sign %s failed, not publishing: %v", kind, err)
		return
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
