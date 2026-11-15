package chain

import (
	"context"
	"encoding/json"
	"errors"
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

// Out-of-order blocks. A block is recorded as seen only after the handler
// accepted it. It used to be recorded as soon as it verified, so a genuine
// block that arrived before its parent (2 before 1) was refused once and then
// skipped for the life of the process, every later copy included, catch-up
// block_response copies too: a P2P-only follower stayed at the parent's height
// until a restart.
//
// A verified block is now in one of four states:
//   - in flight: the handler is working on it;
//   - seen: the handler accepted it;
//   - waiting: the handler found it ahead of the tip (ErrExternalBlockAhead).
//     It waits in a bounded queue and is tried again as soon as its parent is
//     accepted, or on RetryPending when the tip moved some other way;
//   - held back: the handler refused it for another reason. Copies are
//     dropped until the next catch-up round (RetryPending) or at most
//     blockRetryAfter; then a copy is verified and tried again.
//
// Copies of a block in any of the four states cost a map lookup. No state is
// entered before the block verifies, so a forged copy cannot take a genuine
// block's place. A genuine block that keeps failing costs one verification
// and one handler call per round, not one per copy.
//
// The queue takes blocks at most one catch-up window (maxResponseBlocks, 64 by
// default) above the tip, and at most that many blocks; when it is full, the
// blocks nearest the tip stay. A block that does not fit is held back and comes
// again with the catch-up window that covers it.

// blockRetryAfter is the longest a held-back block is held. It is the
// catch-up interval in cmd/qsdm, whose rounds normally lift the hold sooner.
const blockRetryAfter = 10 * time.Second

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
	seen              map[string]time.Time // accepted block hash -> time accepted
	seenOrder         []string             // seen hashes, oldest first
	seenCap           int                  // 0: blockSeenCap
	inflight          map[string]struct{}  // verified, with the handler now
	waiting           map[string]*Block    // verified, ahead of the tip (bounded)
	held              blockHolds           // verified, refused: held back for a round
	now               func() time.Time     // nil: time.Now (tests use a fake clock)
	stats             BlockPropagationStats
	mu                sync.Mutex
	ctx               context.Context
	cancel            context.CancelFunc
}

// BlockPropagationStats counts what happened to the blocks received from
// peers, and the current size of each state.
type BlockPropagationStats struct {
	Duplicates uint64 // copies dropped by a lookup: seen, in flight, waiting or held back
	Invalid    uint64 // failed verification
	Accepted   uint64 // accepted by the handler (a block already on the chain included)
	FromQueue  uint64 // of those, accepted after waiting for the parent
	Queued     uint64 // ahead of the tip, put in the queue
	NotQueued  uint64 // ahead of the tip, but outside the window or pushed out of a full queue
	HeldBack   uint64 // refused by the handler, held back for a round

	Seen     int // current sizes
	InFlight int
	Waiting  int
	Held     int
}

// blockHolds is the held-back set. A hold lasts at most blockRetryAfter, or
// until clear. Two generations give O(1) work per block and at most limit
// entries: under a flood the holds get shorter, never the set larger.
type blockHolds struct {
	cur, prev map[string]time.Time // hash -> when the hold started
	since     time.Time            // when cur was started
}

func (h *blockHolds) rotate(now time.Time) {
	if h.cur != nil && !now.Before(h.since) && now.Sub(h.since) < blockRetryAfter {
		return
	}
	if h.cur != nil && now.Before(h.since) {
		h.cur = nil // the clock went back: start over
	}
	// Every hold in prev started before since, so it has expired by now.
	h.prev = h.cur
	h.cur = make(map[string]time.Time)
	h.since = now
}

func (h *blockHolds) has(hash string, now time.Time) bool {
	h.rotate(now)
	if at, ok := h.cur[hash]; ok && now.Sub(at) < blockRetryAfter {
		return true
	}
	if at, ok := h.prev[hash]; ok && now.Sub(at) < blockRetryAfter {
		return true
	}
	return false
}

func (h *blockHolds) add(hash string, now time.Time, limit int) {
	h.rotate(now)
	if half := limit / 2; len(h.cur) >= half || half == 0 {
		h.prev = h.cur
		h.cur = make(map[string]time.Time)
		h.since = now
	}
	h.cur[hash] = now
}

func (h *blockHolds) clear() {
	h.cur, h.prev = nil, nil
}

func (h *blockHolds) size() int {
	return len(h.cur) + len(h.prev)
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
	// here. A block enters a state (in flight, seen, waiting, held back) only
	// after it verifies, so a forged block cannot take the place of a genuine
	// one.
	if bp.known(block.Hash) {
		return
	}
	if !bp.validateBlock(block) {
		bp.mu.Lock()
		bp.stats.Invalid++
		bp.mu.Unlock()
		log.Printf("[block-prop] rejected invalid block %d from %s", block.Height, originNode)
		return
	}
	if !bp.claim(block.Hash) {
		return
	}
	if bp.process(block, false) == blockAccepted {
		bp.drainAfter(block.Height)
	}
}

type blockOutcome int

const (
	blockAccepted blockOutcome = iota // the handler accepted it: seen
	blockWaiting                      // ahead of the tip: in the queue
	blockRefused                      // held back for a round
)

func (bp *BlockPropagator) clock() time.Time {
	if bp.now != nil {
		return bp.now()
	}
	return time.Now()
}

func (bp *BlockPropagator) seenLimitLocked() int {
	if bp.seenCap > 0 {
		return bp.seenCap
	}
	return blockSeenCap
}

// knownLocked reports whether hash is seen, in flight, waiting or held back.
func (bp *BlockPropagator) knownLocked(hash string) bool {
	if _, ok := bp.seen[hash]; ok {
		return true
	}
	if _, ok := bp.inflight[hash]; ok {
		return true
	}
	if _, ok := bp.waiting[hash]; ok {
		return true
	}
	return bp.held.has(hash, bp.clock())
}

// known is the cheap pre-check: a lookup, before any verification.
func (bp *BlockPropagator) known(hash string) bool {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if bp.knownLocked(hash) {
		bp.stats.Duplicates++
		return true
	}
	return false
}

// claim puts a verified block in flight, unless another copy got there first.
func (bp *BlockPropagator) claim(hash string) bool {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if bp.knownLocked(hash) {
		bp.stats.Duplicates++
		return false
	}
	if bp.inflight == nil {
		bp.inflight = make(map[string]struct{})
	}
	bp.inflight[hash] = struct{}{}
	return true
}

// process hands a claimed (in flight) block to the handler and moves it to
// the state the result calls for.
func (bp *BlockPropagator) process(block *Block, fromQueue bool) blockOutcome {
	var err error
	if bp.handler != nil {
		err = bp.handler(block)
	}
	var ahead *ExternalBlockAheadError
	isAhead := err != nil && errors.As(err, &ahead) && ahead != nil

	bp.mu.Lock()
	delete(bp.inflight, block.Hash)
	outcome := blockRefused
	switch {
	case err == nil:
		bp.markSeenLocked(block.Hash)
		bp.stats.Accepted++
		if fromQueue {
			bp.stats.FromQueue++
		}
		outcome = blockAccepted
	case isAhead && bp.enqueueLocked(block, ahead.Tip):
		if !fromQueue {
			bp.stats.Queued++
		}
		outcome = blockWaiting
	default:
		if isAhead {
			bp.stats.NotQueued++
		}
		bp.held.add(block.Hash, bp.clock(), bp.seenLimitLocked())
		bp.stats.HeldBack++
	}
	bp.mu.Unlock()

	switch {
	case outcome == blockWaiting && !fromQueue:
		log.Printf("[block-prop] block %d is ahead of tip %d; it waits for its parent", block.Height, ahead.Tip)
	case outcome == blockRefused:
		log.Printf("[block-prop] handler error for block %d: %v", block.Height, err)
	}
	return outcome
}

// queueWindowLocked is how far above the tip a block may wait, and how many
// blocks may wait: one catch-up window.
func (bp *BlockPropagator) queueWindowLocked() uint64 {
	if bp.maxResponseBlocks > 0 {
		return uint64(bp.maxResponseBlocks)
	}
	return defaultBlockResponseLimit
}

// enqueueLocked puts a block that is ahead of tip in the queue, if it is
// within the window and fits. In a full queue the blocks nearest the tip stay;
// a block pushed out is held back and comes again with a later catch-up window.
func (bp *BlockPropagator) enqueueLocked(block *Block, tip uint64) bool {
	window := bp.queueWindowLocked()
	if block.Height <= tip || block.Height-tip > window {
		return false
	}
	if bp.waiting == nil {
		bp.waiting = make(map[string]*Block)
	}
	if uint64(len(bp.waiting)) >= window {
		var farthest *Block
		for _, w := range bp.waiting {
			if farthest == nil || w.Height > farthest.Height {
				farthest = w
			}
		}
		if farthest == nil || block.Height >= farthest.Height {
			return false
		}
		delete(bp.waiting, farthest.Hash)
		bp.held.add(farthest.Hash, bp.clock(), bp.seenLimitLocked())
		bp.stats.NotQueued++
	}
	bp.waiting[block.Hash] = block
	return true
}

// takeWaiting moves the waiting blocks at height into flight.
func (bp *BlockPropagator) takeWaiting(height uint64) []*Block {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	var batch []*Block
	for hash, w := range bp.waiting {
		if w.Height != height {
			continue
		}
		delete(bp.waiting, hash)
		if bp.inflight == nil {
			bp.inflight = make(map[string]struct{})
		}
		bp.inflight[hash] = struct{}{}
		batch = append(batch, w)
	}
	return batch
}

// drainAfter tries the waiting blocks above an accepted height, in order,
// for as long as each height is accepted.
func (bp *BlockPropagator) drainAfter(height uint64) {
	for height < ^uint64(0) {
		next := height + 1
		batch := bp.takeWaiting(next)
		if len(batch) == 0 {
			return
		}
		accepted := false
		for _, w := range batch {
			if bp.process(w, true) == blockAccepted {
				accepted = true
			}
		}
		if !accepted {
			return
		}
		height = next
	}
}

// RetryPending lifts every hold, so the next copy of a held-back block is
// verified and tried again, and tries the waiting blocks from the lowest
// height. cmd/qsdm calls it once per catch-up round, just before it asks peers
// for the next window. It also covers a tip that moved by another path (HTTP
// chain sync), which the propagator does not see.
func (bp *BlockPropagator) RetryPending() {
	bp.mu.Lock()
	bp.held.clear()
	bp.mu.Unlock()
	for {
		bp.mu.Lock()
		var lowest uint64
		found := false
		for _, w := range bp.waiting {
			if !found || w.Height < lowest {
				lowest, found = w.Height, true
			}
		}
		bp.mu.Unlock()
		if !found {
			return
		}
		batch := bp.takeWaiting(lowest)
		if len(batch) == 0 {
			return
		}
		accepted, stillAhead := false, false
		for _, w := range batch {
			switch bp.process(w, true) {
			case blockAccepted:
				accepted = true
			case blockWaiting:
				stillAhead = true
			}
		}
		if accepted {
			bp.drainAfter(lowest)
			continue
		}
		if stillAhead {
			// Every other waiting block is higher, so it is ahead too.
			return
		}
		// The lowest were refused (held back); try the next height.
	}
}

// Stats returns the counters and the current size of each state.
func (bp *BlockPropagator) Stats() BlockPropagationStats {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	s := bp.stats
	s.Seen = len(bp.seen)
	s.InFlight = len(bp.inflight)
	s.Waiting = len(bp.waiting)
	s.Held = bp.held.size()
	return s
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
	return bp.markSeenLocked(hash)
}

func (bp *BlockPropagator) markSeenLocked(hash string) bool {
	if _, already := bp.seen[hash]; already {
		return false
	}
	if bp.seen == nil {
		bp.seen = make(map[string]time.Time)
	}
	bp.seen[hash] = bp.clock()
	bp.seenOrder = append(bp.seenOrder, hash)
	limit := bp.seenLimitLocked()
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
