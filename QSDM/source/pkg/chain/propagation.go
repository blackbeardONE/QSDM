package chain

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

const BlockTopicName = "qsdm-blocks"
const defaultBlockResponseLimit = 64

// blockSeenCap bounds the seen set (QSDM/docs/docs/P2P_LIMITS.md, risk 4); it used
// to grow for the life of the process. 100,000 hashes is about 11.5 days of
// 10 s blocks, and the oldest are forgotten first. A forgotten block that
// comes round again is verified again and handed to the handler, which
// refuses a block it already has.
const blockSeenCap = 100_000

const (
	BlockMessageNewBlock = "new_block"
	BlockMessageRequest  = "block_request"
	BlockMessageResponse = "block_response"
)

// BlockP2PMessage is the envelope for a propagated block.
type BlockP2PMessage struct {
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	OriginNode string          `json:"origin_node"`
	TargetNode string          `json:"target_node,omitempty"`
	RequestID  string          `json:"request_id,omitempty"`
	Timestamp  string          `json:"ts"`
}

// BlockRequest asks peers for a bounded contiguous block range.
type BlockRequest struct {
	From  uint64 `json:"from"`
	To    uint64 `json:"to"`
	Limit uint64 `json:"limit"`
}

// BlockResponse returns a bounded contiguous block range.
type BlockResponse struct {
	From   uint64   `json:"from"`
	To     uint64   `json:"to"`
	Blocks []*Block `json:"blocks"`
}

// BlockTopicJoiner can join a pubsub topic (implemented by networking.Network).
type BlockTopicJoiner interface {
	JoinTopic(name string) (*pubsub.Topic, *pubsub.Subscription, error)
}

// BlockHandler processes a received block from a peer.
type BlockHandler func(block *Block) error

// BlockProvider serves locally committed blocks for peer catch-up.
type BlockProvider func(from, to uint64, limit int) []*Block

// BlockPropagator broadcasts produced blocks and receives blocks from peers.
type BlockPropagator struct {
	topic             *pubsub.Topic
	sub               *pubsub.Subscription
	nodeID            string
	handler           BlockHandler
	provider          BlockProvider
	maxResponseBlocks int
	budget            *responseBudget      // nil: created on first block_request
	seen              map[string]time.Time // block hash -> first seen time
	seenOrder         []string             // seen hashes, oldest first
	seenCap           int                  // 0: blockSeenCap
	mu                sync.Mutex
	ctx               context.Context
	cancel            context.CancelFunc
}

// NewBlockPropagator joins the block topic and starts listening.
func NewBlockPropagator(net BlockTopicJoiner, nodeID string, handler BlockHandler) (*BlockPropagator, error) {
	t, s, err := net.JoinTopic(BlockTopicName)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	bp := &BlockPropagator{
		topic:             t,
		sub:               s,
		nodeID:            nodeID,
		handler:           handler,
		maxResponseBlocks: defaultBlockResponseLimit,
		budget:            newResponseBudget(),
		seen:              make(map[string]time.Time),
		ctx:               ctx,
		cancel:            cancel,
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

		bp.handleMessageFrom(envelope, string(msg.GetFrom()))
	}
}

func (bp *BlockPropagator) handleMessage(msg BlockP2PMessage) {
	bp.handleMessageFrom(msg, "")
}

// handleMessageFrom handles one envelope. author is the pubsub author's peer
// ID, which StrictSign authenticates; it keys the block-request budget.
func (bp *BlockPropagator) handleMessageFrom(msg BlockP2PMessage, author string) {
	if msg.TargetNode != "" && msg.TargetNode != bp.nodeID {
		return
	}

	switch msg.Kind {
	case BlockMessageNewBlock:
		var block Block
		if err := json.Unmarshal(msg.Payload, &block); err != nil {
			log.Printf("[block-prop] bad block payload: %v", err)
			return
		}
		bp.handleBlock(&block, msg.OriginNode)

	case BlockMessageRequest:
		bp.handleBlockRequest(msg, author)

	case BlockMessageResponse:
		var response BlockResponse
		if err := json.Unmarshal(msg.Payload, &response); err != nil {
			log.Printf("[block-prop] bad block response payload: %v", err)
			return
		}
		for _, block := range response.Blocks {
			bp.handleBlock(block, msg.OriginNode)
		}
	}
}

func (bp *BlockPropagator) handleBlock(block *Block, originNode string) {
	if block == nil {
		return
	}
	// Dedupe before verifying (QSDM/docs/docs/P2P_LIMITS.md, risk 3): a re-signed
	// copy of a block already handled costs a map lookup, not a hash
	// recompute and a signature check. The claimed hash is only a lookup key
	// here. A block is recorded as seen only after it verifies, so a forged
	// block cannot take the place of a genuine one.
	bp.mu.Lock()
	_, already := bp.seen[block.Hash]
	bp.mu.Unlock()
	if already {
		return
	}
	if !bp.validateBlock(block) {
		log.Printf("[block-prop] rejected invalid block %d from %s", block.Height, originNode)
		return
	}
	if !bp.markSeen(block.Hash) {
		return
	}

	if bp.handler != nil {
		if err := bp.handler(block); err != nil {
			log.Printf("[block-prop] handler error for block %d: %v", block.Height, err)
		}
	}
}

func (bp *BlockPropagator) handleBlockRequest(msg BlockP2PMessage, author string) {
	bp.mu.Lock()
	provider := bp.provider
	topic := bp.topic
	if bp.budget == nil {
		bp.budget = newResponseBudget()
	}
	budget := bp.budget
	bp.mu.Unlock()
	if provider == nil || topic == nil {
		return
	}
	if !budget.allow(author) {
		return
	}
	var request BlockRequest
	if err := json.Unmarshal(msg.Payload, &request); err != nil {
		log.Printf("[block-prop] bad block request payload: %v", err)
		return
	}
	if request.To < request.From {
		request.To = request.From
	}
	limit := bp.responseLimit(request.Limit)
	to := clampBlockRequestTo(request.From, request.To, limit)
	blocks := provider(request.From, to, int(limit))
	if len(blocks) > int(limit) {
		blocks = blocks[:int(limit)]
	}
	if len(blocks) == 0 {
		// Nothing to send. Every node asks for the window above its own tip
		// every 10 s, so an empty reply from each peer would only spend
		// topic bandwidth.
		return
	}
	response := BlockResponse{
		From:   request.From,
		To:     to,
		Blocks: blocks,
	}
	if err := bp.publish(BlockMessageResponse, response, msg.OriginNode, msg.RequestID); err != nil {
		log.Printf("[block-prop] block response publish failed: %v", err)
	}
}

// BroadcastBlock publishes a newly produced block to the network.
func (bp *BlockPropagator) BroadcastBlock(block *Block) error {
	if block == nil {
		return nil
	}
	bp.markSeen(block.Hash)

	return bp.publish(BlockMessageNewBlock, block, "", "")
}

// markSeen records hash and reports whether it was new. Past the cap the
// oldest hashes are forgotten first.
func (bp *BlockPropagator) markSeen(hash string) bool {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if _, already := bp.seen[hash]; already {
		return false
	}
	if bp.seen == nil {
		bp.seen = make(map[string]time.Time)
	}
	bp.seen[hash] = time.Now()
	bp.seenOrder = append(bp.seenOrder, hash)
	limit := bp.seenCap
	if limit <= 0 {
		limit = blockSeenCap
	}
	for len(bp.seen) > limit && len(bp.seenOrder) > 0 {
		delete(bp.seen, bp.seenOrder[0])
		bp.seenOrder = bp.seenOrder[1:]
	}
	return true
}

// RequestBlocks asks connected peers for the next contiguous block window.
func (bp *BlockPropagator) RequestBlocks(from, to uint64, limit int) error {
	if to < from {
		to = from
	}
	if limit <= 0 {
		limit = bp.maxResponseBlocks
	}
	if bp.maxResponseBlocks > 0 && limit > bp.maxResponseBlocks {
		limit = bp.maxResponseBlocks
	}
	req := BlockRequest{
		From:  from,
		To:    clampBlockRequestTo(from, to, uint64(limit)),
		Limit: uint64(limit),
	}
	return bp.publish(BlockMessageRequest, req, "", time.Now().UTC().Format("20060102T150405.000000000Z"))
}

func (bp *BlockPropagator) publish(kind string, payloadValue interface{}, targetNode string, requestID string) error {
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return err
	}
	envelope := BlockP2PMessage{
		Kind:       kind,
		Payload:    payload,
		OriginNode: bp.nodeID,
		TargetNode: targetNode,
		RequestID:  requestID,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return bp.topic.Publish(bp.ctx, data)
}

// SetBlockProvider wires the local block store into peer catch-up responses.
func (bp *BlockPropagator) SetBlockProvider(provider BlockProvider) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.provider = provider
}

// SetMaxResponseBlocks bounds the number of blocks returned to any one peer request.
func (bp *BlockPropagator) SetMaxResponseBlocks(limit int) {
	if limit <= 0 {
		limit = defaultBlockResponseLimit
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	bp.maxResponseBlocks = limit
}

// SeenCount returns the number of unique blocks seen.
func (bp *BlockPropagator) SeenCount() int {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	return len(bp.seen)
}

// DroppedBlockResponses returns how many block requests went unanswered
// because they were over the response budget.
func (bp *BlockPropagator) DroppedBlockResponses() uint64 {
	bp.mu.Lock()
	budget := bp.budget
	bp.mu.Unlock()
	if budget == nil {
		return 0
	}
	return budget.droppedCount()
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
	if recomputed != block.Hash {
		return false
	}
	return VerifyBlockSignature(block) == nil
}

func (bp *BlockPropagator) responseLimit(requested uint64) uint64 {
	bp.mu.Lock()
	max := bp.maxResponseBlocks
	bp.mu.Unlock()
	if max <= 0 {
		max = defaultBlockResponseLimit
	}
	if requested == 0 || requested > uint64(max) {
		return uint64(max)
	}
	return requested
}

func clampBlockRequestTo(from, to, limit uint64) uint64 {
	if limit == 0 {
		return from
	}
	maxTo := from
	step := limit - 1
	if step > ^uint64(0)-from {
		maxTo = ^uint64(0)
	} else {
		maxTo = from + step
	}
	if to > maxTo {
		return maxTo
	}
	return to
}

// recomputeHash independently re-derives a block's hash during propagation
// validation.
//
// It delegates to computeBlockHash rather than reimplementing it. It used to
// carry its own copy, including its own tx-root loop that merkleized tx.ID
// unconditionally -- so when computeTxRoot gained the height-gated content
// root, this copy did not. Activating that gate would then have made the
// producer's canonical hash and this validator's recomputed hash disagree for
// every block at or above the activation height, and every node would have
// rejected every valid block: a network-wide propagation halt, caused by the
// mechanism meant to close a consensus defect.
//
// Duplicating a hash derivation is the whole problem. One definition, so the
// two cannot drift again.
func recomputeHash(b *Block) string {
	return computeBlockHash(b)
}
