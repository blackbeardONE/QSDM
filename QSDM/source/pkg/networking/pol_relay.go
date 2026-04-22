package networking

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	pubsub "github.com/libp2p/go-libp2p-pubsub"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// PolTopicName is the GossipSub topic for prevote-lock proofs and round certificates.
const PolTopicName = "qsdm-prevote-lock"

// PolP2PRelay subscribes to POL gossip and forwards payloads to PolGossipIngress.
type PolP2PRelay struct {
	topic   *pubsub.Topic
	sub     *pubsub.Subscription
	ingress *PolGossipIngress
	ctx     context.Context
	cancel  context.CancelFunc
	selfID  string
}

// NewPolP2PRelay joins the POL topic and starts the read loop.
func NewPolP2PRelay(net TopicJoiner, ingress *PolGossipIngress, selfPeerID string) (*PolP2PRelay, error) {
	if ingress == nil {
		return nil, fmt.Errorf("pol gossip ingress is nil")
	}
	t, s, err := net.JoinTopic(PolTopicName)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &PolP2PRelay{
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

func (r *PolP2PRelay) readLoop() {
	for {
		msg, err := r.sub.Next(r.ctx)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			log.Printf("[pol-relay] pubsub read error: %v", err)
			continue
		}
		if msg.ReceivedFrom.String() == r.selfID {
			continue
		}
		peer := msg.ReceivedFrom.String()
		if err := r.ingress.HandlePeerMessage(peer, msg.Data); err != nil {
			log.Printf("[pol-relay] ingress from %s: %v", peer, err)
		}
	}
}

func marshalPolWire(kind string, payload json.RawMessage) ([]byte, error) {
	return json.Marshal(polGossipWire{Kind: kind, Payload: payload})
}

// PublishPrevoteLockProof publishes a prevote-lock proof JSON envelope.
func (r *PolP2PRelay) PublishPrevoteLockProof(p *chain.PrevoteLockProof) error {
	if r == nil || r.topic == nil || p == nil {
		return nil
	}
	inner, err := chain.EncodePrevoteLockProof(p)
	if err != nil || len(inner) == 0 {
		return err
	}
	b, err := marshalPolWire(polKindPrevoteLock, inner)
	if err != nil {
		return err
	}
	return r.topic.Publish(r.ctx, b)
}

// PublishRoundCertificate publishes a round certificate JSON envelope.
func (r *PolP2PRelay) PublishRoundCertificate(c *chain.RoundCertificate) error {
	if r == nil || r.topic == nil || c == nil {
		return nil
	}
	inner, err := json.Marshal(c)
	if err != nil {
		return err
	}
	b, err := marshalPolWire(polKindRoundCertificate, inner)
	if err != nil {
		return err
	}
	return r.topic.Publish(r.ctx, b)
}

// Close stops the relay read loop.
func (r *PolP2PRelay) Close() {
	if r == nil {
		return
	}
	r.cancel()
}
