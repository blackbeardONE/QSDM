package bridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
)

// TestLiveP2PRelay_LockPropagation boots two real libp2p nodes, creates a lock
// on node A, publishes it, and verifies it appears on node B.
func TestLiveP2PRelay_LockPropagation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Start two libp2p hosts
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

	// Connect B -> A
	err = hostB.Connect(ctx, peer.AddrInfo{
		ID:    hostA.ID(),
		Addrs: hostA.Addrs(),
	})
	if err != nil {
		t.Fatalf("connect B->A: %v", err)
	}

	// Create GossipSub on both
	psA, err := pubsub.NewGossipSub(ctx, hostA)
	if err != nil {
		t.Fatalf("pubsub A: %v", err)
	}
	psB, err := pubsub.NewGossipSub(ctx, hostB)
	if err != nil {
		t.Fatalf("pubsub B: %v", err)
	}

	// Node A: publisher
	topicA, err := psA.Join(BridgeTopicName)
	if err != nil {
		t.Fatalf("join A: %v", err)
	}
	_, err = topicA.Subscribe() // must subscribe to send
	if err != nil {
		t.Fatalf("sub A: %v", err)
	}

	// Node B: bridge protocol + relay
	bpB := &BridgeProtocol{locks: make(map[string]*Lock)}
	topicB, err := psB.Join(BridgeTopicName)
	if err != nil {
		t.Fatalf("join B: %v", err)
	}
	subB, err := topicB.Subscribe()
	if err != nil {
		t.Fatalf("sub B: %v", err)
	}

	// Start relay read loop for node B
	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()
	relayB := &P2PRelay{
		topic:  topicB,
		sub:    subB,
		bridge: bpB,
		ctx:    relayCtx,
		cancel: relayCancel,
	}
	go relayB.readLoop(hostB.ID().String())

	// Give GossipSub time to establish mesh
	time.Sleep(500 * time.Millisecond)

	// Node A publishes a lock event
	lock := Lock{
		ID:          "live-lock-001",
		SourceChain: "chain-alpha",
		TargetChain: "chain-beta",
		Asset:       "TOKEN",
		Amount:      42.0,
		SecretHash:  "abc123",
		Secret:      "should-be-stripped",
		Status:      LockStatusLocked,
	}

	safe := lock
	safe.Secret = ""
	payload, _ := json.Marshal(safe)

	evt := BridgeP2PEvent{
		Kind:       "lock_created",
		Payload:    payload,
		OriginNode: hostA.ID().String(),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(evt)

	err = topicA.Publish(ctx, data)
	if err != nil {
		t.Fatalf("publish from A: %v", err)
	}

	// Poll node B for the lock
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for lock to propagate from A to B")
		case <-ticker.C:
			bpB.mu.RLock()
			l, ok := bpB.locks["live-lock-001"]
			bpB.mu.RUnlock()
			if ok {
				if l.Amount != 42.0 {
					t.Errorf("lock amount = %f, want 42", l.Amount)
				}
				if l.Secret != "" {
					t.Errorf("secret should be empty on receiver, got %q", l.Secret)
				}
				if l.SecretHash != "abc123" {
					t.Errorf("hash = %q, want abc123", l.SecretHash)
				}
				t.Logf("lock propagated from %s to %s in <10s", hostA.ID().String()[:12], hostB.ID().String()[:12])
				return
			}
		}
	}
}

// TestLiveP2PRelay_SwapPropagation verifies swap events propagate between live nodes.
func TestLiveP2PRelay_SwapPropagation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	psA, _ := pubsub.NewGossipSub(ctx, hostA)
	psB, _ := pubsub.NewGossipSub(ctx, hostB)

	topicA, _ := psA.Join(BridgeTopicName)
	topicA.Subscribe()

	aspB := &AtomicSwapProtocol{swaps: make(map[string]*Swap)}
	topicB, _ := psB.Join(BridgeTopicName)
	subB, _ := topicB.Subscribe()

	relayCtx, relayCancel := context.WithCancel(ctx)
	defer relayCancel()
	relayB := &P2PRelay{
		topic:  topicB,
		sub:    subB,
		swap:   aspB,
		ctx:    relayCtx,
		cancel: relayCancel,
	}
	go relayB.readLoop(hostB.ID().String())

	time.Sleep(500 * time.Millisecond)

	swap := Swap{
		ID:                "live-swap-001",
		InitiatorChain:    "chain-x",
		ParticipantChain:  "chain-y",
		InitiatorAmount:   100,
		ParticipantAmount: 200,
		Status:            SwapStatusInitiated,
	}
	safe := swap
	safe.InitiatorSecret = ""
	safe.ParticipantSecret = ""
	payload, _ := json.Marshal(safe)

	evt := BridgeP2PEvent{
		Kind:       "swap_initiated",
		Payload:    payload,
		OriginNode: hostA.ID().String(),
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(evt)
	topicA.Publish(ctx, data)

	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for swap to propagate")
		case <-ticker.C:
			aspB.mu.RLock()
			s, ok := aspB.swaps["live-swap-001"]
			aspB.mu.RUnlock()
			if ok {
				if s.InitiatorAmount != 100 {
					t.Errorf("initiator amount = %f, want 100", s.InitiatorAmount)
				}
				t.Logf("swap propagated between live nodes")
				return
			}
		}
	}
}

// TestLiveP2PRelay_ThreeNodes tests lock event propagation across 3 connected nodes.
func TestLiveP2PRelay_ThreeNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live P2P test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Create 3 hosts
	h0, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("host 0: %v", err)
	}
	defer h0.Close()

	h1, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("host 1: %v", err)
	}
	defer h1.Close()

	h2, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("host 2: %v", err)
	}
	defer h2.Close()

	// Star topology: 1->0, 2->0
	if err := h1.Connect(ctx, peer.AddrInfo{ID: h0.ID(), Addrs: h0.Addrs()}); err != nil {
		t.Fatalf("connect 1->0: %v", err)
	}
	if err := h2.Connect(ctx, peer.AddrInfo{ID: h0.ID(), Addrs: h0.Addrs()}); err != nil {
		t.Fatalf("connect 2->0: %v", err)
	}

	// GossipSub on each
	ps0, _ := pubsub.NewGossipSub(ctx, h0)
	ps1, _ := pubsub.NewGossipSub(ctx, h1)
	ps2, _ := pubsub.NewGossipSub(ctx, h2)

	// Node 0: publisher
	topic0, _ := ps0.Join(BridgeTopicName)
	topic0.Subscribe()

	// Nodes 1 and 2: receivers with bridge protocols
	bp1 := &BridgeProtocol{locks: make(map[string]*Lock)}
	topic1, _ := ps1.Join(BridgeTopicName)
	sub1, _ := topic1.Subscribe()
	ctx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()
	relay1 := &P2PRelay{topic: topic1, sub: sub1, bridge: bp1, ctx: ctx1, cancel: cancel1}
	go relay1.readLoop(h1.ID().String())

	bp2 := &BridgeProtocol{locks: make(map[string]*Lock)}
	topic2, _ := ps2.Join(BridgeTopicName)
	sub2, _ := topic2.Subscribe()
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	relay2 := &P2PRelay{topic: topic2, sub: sub2, bridge: bp2, ctx: ctx2, cancel: cancel2}
	go relay2.readLoop(h2.ID().String())

	time.Sleep(700 * time.Millisecond)

	// Publish lock from node 0
	lock := Lock{ID: "3node-lock", Amount: 77, SecretHash: "h3", Status: LockStatusLocked}
	payload, _ := json.Marshal(lock)
	evt := BridgeP2PEvent{Kind: "lock_created", Payload: payload, OriginNode: h0.ID().String()}
	data, _ := json.Marshal(evt)
	topic0.Publish(ctx, data)

	// Wait for both nodes to receive
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	got1, got2 := false, false
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout: node1=%v node2=%v", got1, got2)
		case <-ticker.C:
			if !got1 {
				bp1.mu.RLock()
				_, got1 = bp1.locks["3node-lock"]
				bp1.mu.RUnlock()
			}
			if !got2 {
				bp2.mu.RLock()
				_, got2 = bp2.locks["3node-lock"]
				bp2.mu.RUnlock()
			}
			if got1 && got2 {
				t.Logf("lock propagated to both receivers via star topology")
				return
			}
		}
	}
}
