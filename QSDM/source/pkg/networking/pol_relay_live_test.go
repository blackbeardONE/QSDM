package networking

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestLivePolRelay_Propagation(t *testing.T) {
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

	topicA, err := psA.Join(PolTopicName)
	if err != nil {
		t.Fatalf("join A: %v", err)
	}
	if _, err := topicA.Subscribe(); err != nil {
		t.Fatalf("sub A: %v", err)
	}

	ingressB := NewPolGossipIngress(DefaultPolGossipConfig(), nil)
	jb := &psJoiner{ps: psB}
	relayB, err := NewPolP2PRelay(jb, ingressB, hostB.ID().String())
	if err != nil {
		t.Fatalf("relay B: %v", err)
	}
	defer relayB.Close()

	_, recvSub, err := jb.JoinTopic(PolTopicName)
	if err != nil {
		t.Fatalf("join recv: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	p := &chain.PrevoteLockProof{
		Height:          7,
		Round:           0,
		LockedBlockHash: "state-root-x",
	}
	inner, err := chain.EncodePrevoteLockProof(p)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(polGossipWire{Kind: polKindPrevoteLock, Payload: inner})
	if err != nil {
		t.Fatal(err)
	}
	if err := topicA.Publish(ctx, body); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.After(12 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for POL message on B")
		default:
		}
		msg, err := recvSub.Next(ctx)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if msg.ReceivedFrom.String() == hostB.ID().String() {
			continue
		}
		var wire polGossipWire
		if err := json.Unmarshal(msg.Data, &wire); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if wire.Kind != polKindPrevoteLock {
			t.Fatalf("kind: %s", wire.Kind)
		}
		pl, err := chain.DecodePrevoteLockProof(wire.Payload)
		if err != nil || pl.Height != 7 {
			t.Fatalf("proof decode: %v %#v", err, pl)
		}
		break
	}
}
