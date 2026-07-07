package chain

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

func setupPropagationTest(t *testing.T) (*pubsub.PubSub, *pubsub.Topic, *pubsub.Subscription) {
	t.Helper()
	ctx := context.Background()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	t.Cleanup(func() { h.Close() })

	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		t.Fatalf("create pubsub: %v", err)
	}
	topic, err := ps.Join(BlockTopicName)
	if err != nil {
		t.Fatalf("join topic: %v", err)
	}
	sub, err := topic.Subscribe()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return ps, topic, sub
}

func TestBlockP2PMessage_MarshalUnmarshal(t *testing.T) {
	block := makeBlock(5)
	block.Hash = computeBlockHash(block)
	payload, _ := json.Marshal(block)

	msg := BlockP2PMessage{
		Kind:       "new_block",
		Payload:    payload,
		OriginNode: "node-1",
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded BlockP2PMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Kind != "new_block" {
		t.Fatalf("expected new_block, got %s", decoded.Kind)
	}

	var decodedBlock Block
	json.Unmarshal(decoded.Payload, &decodedBlock)
	if decodedBlock.Height != 5 {
		t.Fatalf("expected height 5, got %d", decodedBlock.Height)
	}
}

func TestBlockPropagator_ValidateBlock(t *testing.T) {
	bp := &BlockPropagator{seen: make(map[string]time.Time)}

	block := makeBlock(1)
	block.Hash = computeBlockHash(block)

	if !bp.validateBlock(block) {
		t.Fatal("valid block should pass validation")
	}

	// Tamper with the hash
	badBlock := makeBlock(2)
	badBlock.Hash = "tampered"
	if bp.validateBlock(badBlock) {
		t.Fatal("tampered block should fail validation")
	}

	// Missing hash
	noHash := makeBlock(3)
	noHash.Hash = ""
	if bp.validateBlock(noHash) {
		t.Fatal("block with empty hash should fail")
	}
}

func TestBlockPropagator_DeduplicateBlocks(t *testing.T) {
	var mu sync.Mutex
	var received []Block

	handler := func(block *Block) error {
		mu.Lock()
		received = append(received, *block)
		mu.Unlock()
		return nil
	}

	bp := &BlockPropagator{
		seen:    make(map[string]time.Time),
		handler: handler,
	}

	block := makeBlock(0)
	block.Hash = computeBlockHash(block)
	payload, _ := json.Marshal(block)

	msg := BlockP2PMessage{
		Kind:       "new_block",
		Payload:    payload,
		OriginNode: "remote",
	}

	// First time: should be handled
	bp.handleMessage(msg)
	// Second time: should be deduplicated
	bp.handleMessage(msg)

	mu.Lock()
	count := len(received)
	mu.Unlock()

	if count != 1 {
		t.Fatalf("expected 1 block (dedup), got %d", count)
	}
}

func TestBlockPropagator_RejectInvalidBlock(t *testing.T) {
	var received int
	bp := &BlockPropagator{
		seen:    make(map[string]time.Time),
		handler: func(block *Block) error { received++; return nil },
	}

	block := makeBlock(1)
	block.Hash = "invalid_hash"
	payload, _ := json.Marshal(block)

	msg := BlockP2PMessage{
		Kind:       "new_block",
		Payload:    payload,
		OriginNode: "remote",
	}

	bp.handleMessage(msg)
	if received != 0 {
		t.Fatal("invalid block should not reach handler")
	}
}

func TestBlockPropagator_BlockResponseTargetsNode(t *testing.T) {
	var mu sync.Mutex
	var received []uint64

	bp := &BlockPropagator{
		nodeID: "local",
		seen:   make(map[string]time.Time),
		handler: func(block *Block) error {
			mu.Lock()
			defer mu.Unlock()
			received = append(received, block.Height)
			return nil
		},
	}

	block := makeBlock(7)
	block.Hash = computeBlockHash(block)
	responsePayload, _ := json.Marshal(BlockResponse{
		From:   7,
		To:     7,
		Blocks: []*Block{block},
	})

	bp.handleMessage(BlockP2PMessage{
		Kind:       BlockMessageResponse,
		Payload:    responsePayload,
		OriginNode: "remote",
		TargetNode: "someone-else",
	})
	if len(received) != 0 {
		t.Fatal("response for another node should be ignored")
	}

	bp.handleMessage(BlockP2PMessage{
		Kind:       BlockMessageResponse,
		Payload:    responsePayload,
		OriginNode: "remote",
		TargetNode: "local",
	})

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || received[0] != 7 {
		t.Fatalf("expected block 7 to be handled once, got %v", received)
	}
}

func TestBlockPropagator_SeenCount(t *testing.T) {
	bp := &BlockPropagator{seen: make(map[string]time.Time)}

	if bp.SeenCount() != 0 {
		t.Fatal("expected 0")
	}

	block := makeBlock(0)
	block.Hash = computeBlockHash(block)

	bp.mu.Lock()
	bp.seen[block.Hash] = time.Now()
	bp.mu.Unlock()

	if bp.SeenCount() != 1 {
		t.Fatal("expected 1")
	}
}
