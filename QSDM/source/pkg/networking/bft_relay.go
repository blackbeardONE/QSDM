package networking

import (
	"context"
	"fmt"
	"log"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// BFTP2PRelay subscribes to BFT gossip and forwards payloads to BFTGossipIngress.
type BFTP2PRelay struct {
	topic   *pubsub.Topic
	sub     *pubsub.Subscription
	ingress *BFTGossipIngress
	ctx     context.Context
	cancel  context.CancelFunc
	selfID  string
}

// NewBFTP2PRelay joins the BFT topic and starts the read loop.
func NewBFTP2PRelay(net TopicJoiner, ingress *BFTGossipIngress, selfPeerID string) (*BFTP2PRelay, error) {
	if ingress == nil {
		return nil, fmt.Errorf("bft gossip ingress is nil")
	}
	t, s, err := net.JoinTopic(BFTTopicName)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &BFTP2PRelay{
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

func (r *BFTP2PRelay) readLoop() {
	for {
		msg, err := r.sub.Next(r.ctx)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			log.Printf("[bft-relay] pubsub read error: %v", err)
			continue
		}
		if msg.ReceivedFrom.String() == r.selfID {
			continue
		}
		peer := msg.ReceivedFrom.String()
		if err := r.ingress.HandlePeerMessage(peer, msg.Data); err != nil {
			log.Printf("[bft-relay] ingress from %s: %v", peer, err)
		}
	}
}

// PublishRaw publishes an already-encoded BFT wire payload (e.g. from chain.MarshalBFTWire).
func (r *BFTP2PRelay) PublishRaw(b []byte) error {
	if r == nil || r.topic == nil || len(b) == 0 {
		return nil
	}
	return r.topic.Publish(r.ctx, b)
}

// Close stops the relay read loop.
func (r *BFTP2PRelay) Close() {
	if r == nil {
		return
	}
	r.cancel()
}
