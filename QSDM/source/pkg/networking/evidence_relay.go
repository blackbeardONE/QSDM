package networking

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// TopicJoiner is the minimal surface to join a gossip topic (implemented by *Network).
type TopicJoiner interface {
	JoinTopic(name string) (*pubsub.Topic, *pubsub.Subscription, error)
}

// EvidenceTopicName is the GossipSub topic for consensus evidence propagation.
const EvidenceTopicName = "qsdm-evidence"

// EvidenceP2PRelay subscribes to evidence gossip and forwards payloads to EvidenceGossipIngress.
type EvidenceP2PRelay struct {
	topic   *pubsub.Topic
	sub     *pubsub.Subscription
	ingress *EvidenceGossipIngress
	ctx     context.Context
	cancel  context.CancelFunc
	selfID  string
}

// NewEvidenceP2PRelay joins the evidence topic and starts the read loop.
func NewEvidenceP2PRelay(net TopicJoiner, ingress *EvidenceGossipIngress, selfPeerID string) (*EvidenceP2PRelay, error) {
	if ingress == nil {
		return nil, fmt.Errorf("evidence ingress is nil")
	}
	t, s, err := net.JoinTopic(EvidenceTopicName)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &EvidenceP2PRelay{
		topic:   t,
		sub:     s,
		ingress: ingress,
		ctx:     ctx,
		cancel:  cancel,
		selfID:  selfPeerID,
	}
	go r.readLoop()
	return r, nil
}

func (r *EvidenceP2PRelay) readLoop() {
	for {
		msg, err := r.sub.Next(r.ctx)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			log.Printf("[evidence-relay] pubsub read error: %v", err)
			continue
		}
		if msg.ReceivedFrom.String() == r.selfID {
			continue
		}
		peer := msg.ReceivedFrom.String()
		if err := r.ingress.HandlePeerMessage(peer, msg.Data); err != nil {
			log.Printf("[evidence-relay] ingress from %s: %v", peer, err)
		}
	}
}

// PublishEvidence marshals evidence as JSON and publishes to the evidence topic.
func (r *EvidenceP2PRelay) PublishEvidence(ev chain.ConsensusEvidence) error {
	if r == nil || r.topic == nil {
		return nil
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return r.topic.Publish(r.ctx, b)
}

// Close stops the relay read loop.
func (r *EvidenceP2PRelay) Close() {
	if r == nil {
		return
	}
	r.cancel()
}
