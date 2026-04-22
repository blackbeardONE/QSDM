package networking

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestLiveEvidenceRelay_Propagation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	hostA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("host A: %v", err)
	}
	defer hostA.Close()

	hostB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("host B: %v", err)
	}
	defer hostB.Close()

	if err := hostB.Connect(ctx, peer.AddrInfo{ID: hostA.ID(), Addrs: hostA.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}

	psA, err := pubsub.NewGossipSub(ctx, hostA)
	if err != nil {
		t.Fatalf("pubsub A: %v", err)
	}
	psB, err := pubsub.NewGossipSub(ctx, hostB)
	if err != nil {
		t.Fatalf("pubsub B: %v", err)
	}

	topicA, err := psA.Join(EvidenceTopicName)
	if err != nil {
		t.Fatalf("join A: %v", err)
	}
	if _, err := topicA.Subscribe(); err != nil {
		t.Fatalf("sub A: %v", err)
	}

	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	em := chain.NewEvidenceManager(vs)
	ingressB := NewEvidenceGossipIngress(em, nil, DefaultEvidenceGossipConfig())

	jb := &psJoiner{ps: psB}
	relayB, err := NewEvidenceP2PRelay(jb, ingressB, hostB.ID().String())
	if err != nil {
		t.Fatalf("relay B: %v", err)
	}
	defer relayB.Close()

	time.Sleep(500 * time.Millisecond)

	ev := chain.ConsensusEvidence{
		Type:      chain.EvidenceInvalidVote,
		Validator: "v1",
		Height:    9,
		Round:     0,
		Details:   "live relay test",
		Timestamp: time.Now().UTC(),
	}
	payload, _ := json.Marshal(ev)
	if err := topicA.Publish(ctx, payload); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for evidence to be processed on B")
		default:
		}
		if em.Stats()["processed"] >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// psJoiner adapts a GossipSub instance to bridge.TopicJoiner for tests.
// It caches joined topics so callers can take additional subscriptions after a relay
// has already joined the same topic (GossipSub rejects a second ps.Join on the same name).
type psJoiner struct {
	ps     *pubsub.PubSub
	mu     sync.Mutex
	topics map[string]*pubsub.Topic
}

func (p *psJoiner) JoinTopic(name string) (*pubsub.Topic, *pubsub.Subscription, error) {
	p.mu.Lock()
	if p.topics == nil {
		p.topics = make(map[string]*pubsub.Topic)
	}
	t, ok := p.topics[name]
	if !ok {
		var err error
		t, err = p.ps.Join(name)
		if err != nil {
			p.mu.Unlock()
			return nil, nil, err
		}
		p.topics[name] = t
	}
	p.mu.Unlock()

	s, err := t.Subscribe()
	if err != nil {
		return nil, nil, err
	}
	return t, s, nil
}
