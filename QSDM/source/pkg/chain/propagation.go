package chain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"sync"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

const BlockTopicName = "qsdm-blocks"

// BlockP2PMessage is the envelope for a propagated block.
type BlockP2PMessage struct {
	Kind       string          `json:"kind"` // "new_block" or "block_request"
	Payload    json.RawMessage `json:"payload"`
	OriginNode string          `json:"origin_node"`
	Timestamp  string          `json:"ts"`
}

// BlockTopicJoiner can join a pubsub topic (implemented by networking.Network).
type BlockTopicJoiner interface {
	JoinTopic(name string) (*pubsub.Topic, *pubsub.Subscription, error)
}

// BlockHandler processes a received block from a peer.
type BlockHandler func(block *Block) error

// BlockPropagator broadcasts produced blocks and receives blocks from peers.
type BlockPropagator struct {
	topic    *pubsub.Topic
	sub      *pubsub.Subscription
	nodeID   string
	handler  BlockHandler
	seen     map[string]time.Time // block hash -> first seen time
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewBlockPropagator joins the block topic and starts listening.
func NewBlockPropagator(net BlockTopicJoiner, nodeID string, handler BlockHandler) (*BlockPropagator, error) {
	t, s, err := net.JoinTopic(BlockTopicName)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	bp := &BlockPropagator{
		topic:   t,
		sub:     s,
		nodeID:  nodeID,
		handler: handler,
		seen:    make(map[string]time.Time),
		ctx:     ctx,
		cancel:  cancel,
	}

	go bp.readLoop()
	return bp, nil
}

func (bp *BlockPropagator) readLoop() {
	for {
		msg, err := bp.sub.Next(bp.ctx)
		if err != nil {
			if bp.ctx.Err() != nil {
				return
			}
			log.Printf("[block-prop] read error: %v", err)
			continue
		}

		var envelope BlockP2PMessage
		if err := json.Unmarshal(msg.Data, &envelope); err != nil {
			log.Printf("[block-prop] malformed message: %v", err)
			continue
		}

		if envelope.OriginNode == bp.nodeID {
			continue
		}

		bp.handleMessage(envelope)
	}
}

func (bp *BlockPropagator) handleMessage(msg BlockP2PMessage) {
	switch msg.Kind {
	case "new_block":
		var block Block
		if err := json.Unmarshal(msg.Payload, &block); err != nil {
			log.Printf("[block-prop] bad block payload: %v", err)
			return
		}

		if !bp.validateBlock(&block) {
			log.Printf("[block-prop] rejected invalid block %d from %s", block.Height, msg.OriginNode)
			return
		}

		bp.mu.Lock()
		if _, already := bp.seen[block.Hash]; already {
			bp.mu.Unlock()
			return // duplicate
		}
		bp.seen[block.Hash] = time.Now()
		bp.mu.Unlock()

		if bp.handler != nil {
			if err := bp.handler(&block); err != nil {
				log.Printf("[block-prop] handler error for block %d: %v", block.Height, err)
			}
		}
	}
}

// BroadcastBlock publishes a newly produced block to the network.
func (bp *BlockPropagator) BroadcastBlock(block *Block) error {
	payload, err := json.Marshal(block)
	if err != nil {
		return err
	}

	bp.mu.Lock()
	bp.seen[block.Hash] = time.Now()
	bp.mu.Unlock()

	envelope := BlockP2PMessage{
		Kind:       "new_block",
		Payload:    payload,
		OriginNode: bp.nodeID,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return bp.topic.Publish(bp.ctx, data)
}

// SeenCount returns the number of unique blocks seen.
func (bp *BlockPropagator) SeenCount() int {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return len(bp.seen)
}

// Close stops the propagator.
func (bp *BlockPropagator) Close() {
	bp.cancel()
}

func (bp *BlockPropagator) validateBlock(block *Block) bool {
	if block.Hash == "" {
		return false
	}
	if block.Height > 0 && block.PrevHash == "" {
		return false
	}

	recomputed := recomputeHash(block)
	return recomputed == block.Hash
}

func recomputeHash(b *Block) string {
	txRoot := ""
	if len(b.Transactions) > 0 {
		ids := make([]string, len(b.Transactions))
		for i, tx := range b.Transactions {
			ids[i] = tx.ID
		}
		tree := BuildMerkleTree(ids)
		txRoot = tree.Root
	} else {
		txRoot = emptyHash()
	}

	data, _ := json.Marshal(struct {
		Height    uint64    `json:"h"`
		PrevHash  string    `json:"p"`
		StateRoot string    `json:"s"`
		TxRoot    string    `json:"t"`
		Time      time.Time `json:"ts"`
		Producer  string    `json:"pr"`
	}{b.Height, b.PrevHash, b.StateRoot, txRoot, b.Timestamp, b.ProducerID})
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
