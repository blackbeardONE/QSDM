package chain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Out-of-order blocks: a block that fails to append must not stay seen.
// orderChain stands in for TryAppendExternalBlock: it accepts a block that
// extends its tip, accepts a block it already has, reports a block above the
// next height as ahead (*ExternalBlockAheadError), and refuses anything else.
type orderChain struct {
	mu       sync.Mutex
	tip      uint64
	byHeight map[uint64]string
	calls    map[string]int
	appended map[string]int
	failOnce map[string]error
	failAll  map[string]error
	gate     map[string]chan struct{}
	entered  chan string
}

func newOrderChain(genesis *Block) *orderChain {
	return &orderChain{
		tip:      genesis.Height,
		byHeight: map[uint64]string{genesis.Height: genesis.Hash},
		calls:    map[string]int{},
		appended: map[string]int{},
		failOnce: map[string]error{},
		failAll:  map[string]error{},
		gate:     map[string]chan struct{}{},
	}
}

func (c *orderChain) append(b *Block) error {
	c.mu.Lock()
	c.calls[b.Hash]++
	gate, entered := c.gate[b.Hash], c.entered
	c.mu.Unlock()
	if gate != nil {
		if entered != nil {
			entered <- b.Hash
		}
		<-gate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, ok := c.failOnce[b.Hash]; ok {
		delete(c.failOnce, b.Hash)
		return err
	}
	if err, ok := c.failAll[b.Hash]; ok {
		return err
	}
	if have, ok := c.byHeight[b.Height]; ok {
		if have == b.Hash {
			return nil
		}
		return &ExternalAppendConflictError{Height: b.Height, ExistingHash: have, NewHash: b.Hash}
	}
	if b.Height > c.tip+1 {
		return &ExternalBlockAheadError{Height: b.Height, Tip: c.tip}
	}
	if b.Height != c.tip+1 || b.PrevHash != c.byHeight[c.tip] {
		return errors.New("chain: external block does not extend tip")
	}
	c.byHeight[b.Height] = b.Hash
	c.tip = b.Height
	c.appended[b.Hash]++
	return nil
}

// appendDirect moves the tip by another path, as HTTP chain sync does.
func (c *orderChain) appendDirect(b *Block) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byHeight[b.Height] = b.Hash
	c.tip = b.Height
	c.appended[b.Hash]++
}

func (c *orderChain) tipHeight() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tip
}

func (c *orderChain) callsFor(b *Block) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[b.Hash]
}

func (c *orderChain) appendedFor(b *Block) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appended[b.Hash]
}

// linkedBlocks returns a hash-linked chain at heights 0..n (unsigned, which
// verifies while signed blocks are not required).
func linkedBlocks(n uint64) []*Block {
	out := make([]*Block, 0, n+1)
	prev := ""
	for h := uint64(0); h <= n; h++ {
		b := makeBlock(h)
		b.PrevHash = prev
		b.Timestamp = time.Unix(1700000000+int64(h), 0).UTC()
		b.Hash = computeBlockHash(b)
		out = append(out, b)
		prev = b.Hash
	}
	return out
}

// forkBlock is a valid block at height that does not link to the chain.
func forkBlock(height uint64, tag string) *Block {
	b := makeBlock(height)
	b.PrevHash = "fork-" + tag
	b.Transactions = []*mempool.Tx{{ID: fmt.Sprintf("fork_%s_%d", tag, height), Sender: "a", Recipient: "b", Amount: 1, Fee: 0.1}}
	b.Timestamp = time.Unix(1800000000+int64(height), 0).UTC()
	b.Hash = computeBlockHash(b)
	return b
}

func newBlockMsg(t *testing.T, b *Block) BlockP2PMessage {
	t.Helper()
	payload, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return BlockP2PMessage{Kind: BlockMessageNewBlock, Payload: payload, OriginNode: "producer"}
}

func responseMsg(t *testing.T, blocks ...*Block) BlockP2PMessage {
	t.Helper()
	payload, err := json.Marshal(BlockResponse{From: blocks[0].Height, To: blocks[len(blocks)-1].Height, Blocks: blocks})
	if err != nil {
		t.Fatal(err)
	}
	return BlockP2PMessage{Kind: BlockMessageResponse, Payload: payload, OriginNode: "producer", TargetNode: "follower"}
}

func newOrderPropagator(c *orderChain) *BlockPropagator {
	return &BlockPropagator{nodeID: "follower", seen: make(map[string]time.Time), handler: c.append}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func expectTip(t *testing.T, c *orderChain, bp *BlockPropagator, want uint64) {
	t.Helper()
	if got := c.tipHeight(); got != want {
		t.Fatalf("tip = %d, want %d (stats %+v)", got, want, bp.Stats())
	}
}

func TestBlockOrder_InOrderReachesTip(t *testing.T) {
	blocks := linkedBlocks(2)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	bp.handleMessage(newBlockMsg(t, blocks[1]))
	bp.handleMessage(newBlockMsg(t, blocks[2]))
	expectTip(t, c, bp, 2)
}

// The reported case: 2, 1, 2 used to stop at 1, because the refused first
// copy of 2 stayed seen and the later copy was skipped.
func TestBlockOrder_Reporter212(t *testing.T) {
	blocks := linkedBlocks(2)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	bp.handleMessage(newBlockMsg(t, blocks[2]))
	if st := bp.Stats(); st.Waiting != 1 || st.Seen != 0 {
		t.Fatalf("block 2 before 1 must wait, not be seen: %+v", st)
	}
	bp.handleMessage(newBlockMsg(t, blocks[1]))
	bp.handleMessage(newBlockMsg(t, blocks[2]))
	expectTip(t, c, bp, 2)
	if n := c.appendedFor(blocks[2]); n != 1 {
		t.Fatalf("block 2 appended %d times, want 1", n)
	}
	if n := c.callsFor(blocks[2]); n != 2 {
		t.Fatalf("block 2 handled %d times, want 2 (ahead, then from the queue)", n)
	}
	st := bp.Stats()
	if st.Queued != 1 || st.FromQueue != 1 || st.Duplicates != 1 || st.Waiting != 0 || st.InFlight != 0 || st.Held != 0 {
		t.Fatalf("stats %+v", st)
	}
}

// The queue alone fills the gap: no further copy of 2 is needed.
func TestBlockOrder_ParentArrivalDrainsQueue(t *testing.T) {
	blocks := linkedBlocks(5)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	for _, h := range []int{5, 3, 2, 4} {
		bp.handleMessage(newBlockMsg(t, blocks[h]))
	}
	expectTip(t, c, bp, 0)
	bp.handleMessage(newBlockMsg(t, blocks[1]))
	expectTip(t, c, bp, 5)
	if st := bp.Stats(); st.Waiting != 0 || st.FromQueue != 4 {
		t.Fatalf("stats %+v", st)
	}
}

func TestBlockOrder_BlockResponseFillsGap(t *testing.T) {
	blocks := linkedBlocks(3)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	bp.handleMessage(newBlockMsg(t, blocks[2]))
	bp.handleMessage(responseMsg(t, blocks[1], blocks[2], blocks[3]))
	expectTip(t, c, bp, 3)

	// A response whose blocks are out of order is handled too.
	blocks2 := linkedBlocks(3)
	c2 := newOrderChain(blocks2[0])
	bp2 := newOrderPropagator(c2)
	bp2.handleMessage(responseMsg(t, blocks2[3], blocks2[2], blocks2[1]))
	expectTip(t, c2, bp2, 3)
}

// The response replay: a block refused once is tried again from a later
// catch-up response. Within the same round the copy is dropped (rate limit).
func TestBlockOrder_BlockResponseReplayAfterRefusal(t *testing.T) {
	blocks := linkedBlocks(2)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	bp.handleMessage(newBlockMsg(t, blocks[1]))
	c.failOnce[blocks[2].Hash] = errors.New("transient: seal guard")
	bp.handleMessage(newBlockMsg(t, blocks[2]))
	expectTip(t, c, bp, 1)
	if st := bp.Stats(); st.Seen != 1 || st.Held != 1 {
		t.Fatalf("a refused block must be held back, not seen: %+v", st)
	}

	bp.handleMessage(responseMsg(t, blocks[2]))
	if n := c.callsFor(blocks[2]); n != 1 {
		t.Fatalf("a copy in the same round must cost no handler call, got %d calls", n)
	}

	bp.RetryPending() // next catch-up round
	bp.handleMessage(responseMsg(t, blocks[1], blocks[2]))
	expectTip(t, c, bp, 2)
}

// Without catch-up rounds a hold still ends after blockRetryAfter.
func TestBlockOrder_HoldExpires(t *testing.T) {
	blocks := linkedBlocks(1)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	clk := &fakeClock{now: time.Unix(1700000000, 0)}
	bp.now = clk.Now
	c.failOnce[blocks[1].Hash] = errors.New("transient")
	bp.handleMessage(newBlockMsg(t, blocks[1]))
	clk.Advance(blockRetryAfter - time.Second)
	bp.handleMessage(newBlockMsg(t, blocks[1]))
	if n := c.callsFor(blocks[1]); n != 1 {
		t.Fatalf("held copy reached the handler: %d calls", n)
	}
	clk.Advance(time.Second)
	bp.handleMessage(newBlockMsg(t, blocks[1]))
	expectTip(t, c, bp, 1)
}

// A block that keeps failing costs one handler call per round, however many
// copies arrive (the rate limit that seen used to give).
func TestBlockOrder_RefusedCopiesCostOneCallPerRound(t *testing.T) {
	blocks := linkedBlocks(1)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	c.failAll[blocks[1].Hash] = errors.New("state_root mismatch after replay")
	msg := newBlockMsg(t, blocks[1])
	for i := 0; i < 50; i++ {
		bp.handleMessage(msg)
	}
	if n := c.callsFor(blocks[1]); n != 1 {
		t.Fatalf("round 1: %d handler calls, want 1", n)
	}
	if st := bp.Stats(); st.Duplicates != 49 || st.Seen != 0 {
		t.Fatalf("stats %+v", st)
	}
	bp.RetryPending()
	for i := 0; i < 50; i++ {
		bp.handleMessage(msg)
	}
	if n := c.callsFor(blocks[1]); n != 2 {
		t.Fatalf("round 2: %d handler calls, want 2", n)
	}
}

// A flood of genuine blocks ahead of the tip: the queue never holds more than
// one window, and the follower still catches up.
func TestBlockOrder_QueueBoundedUnderFlood(t *testing.T) {
	for _, order := range []string{"ascending", "descending"} {
		t.Run(order, func(t *testing.T) {
			blocks := linkedBlocks(600)
			c := newOrderChain(blocks[0])
			bp := newOrderPropagator(c)
			maxWaiting := 0
			send := func(h int) {
				bp.handleMessage(newBlockMsg(t, blocks[h]))
				if w := bp.Stats().Waiting; w > maxWaiting {
					maxWaiting = w
				}
			}
			if order == "ascending" {
				for h := 2; h <= 600; h++ {
					send(h)
				}
			} else {
				for h := 600; h >= 2; h-- {
					send(h)
				}
			}
			if maxWaiting > defaultBlockResponseLimit {
				t.Fatalf("queue held %d blocks, bound %d", maxWaiting, defaultBlockResponseLimit)
			}
			st := bp.Stats()
			if st.Waiting != 63 || st.NotQueued != 600-64 {
				t.Fatalf("want heights 2..64 waiting and the rest held back: %+v", st)
			}
			bp.handleMessage(newBlockMsg(t, blocks[1]))
			expectTip(t, c, bp, 64)
			// The next catch-up round brings the next window.
			bp.RetryPending()
			bp.handleMessage(responseMsg(t, blocks[65:129]...))
			expectTip(t, c, bp, 128)
		})
	}
}

// A full queue keeps the blocks nearest the tip.
func TestBlockOrder_FullQueueKeepsNearest(t *testing.T) {
	blocks := linkedBlocks(4)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	bp.SetMaxResponseBlocks(4)
	bp.handleMessage(newBlockMsg(t, blocks[4]))
	bp.handleMessage(newBlockMsg(t, blocks[3]))
	bp.handleMessage(newBlockMsg(t, forkBlock(3, "x")))
	bp.handleMessage(newBlockMsg(t, forkBlock(3, "y")))
	if st := bp.Stats(); st.Waiting != 4 {
		t.Fatalf("queue should be full: %+v", st)
	}
	bp.handleMessage(newBlockMsg(t, blocks[2]))
	st := bp.Stats()
	if st.Waiting != 4 || st.NotQueued != 1 || st.Held != 1 {
		t.Fatalf("block 2 should push out block 4: %+v", st)
	}
	bp.handleMessage(newBlockMsg(t, blocks[1]))
	expectTip(t, c, bp, 3)
	if st := bp.Stats(); st.Waiting != 0 || st.InFlight != 0 {
		t.Fatalf("stats %+v", st)
	}
	bp.RetryPending()
	bp.handleMessage(newBlockMsg(t, blocks[4]))
	expectTip(t, c, bp, 4)
}

// A block exactly one window above the tip waits; one more does not.
func TestBlockOrder_WindowEdge(t *testing.T) {
	blocks := linkedBlocks(5)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	bp.SetMaxResponseBlocks(4)
	bp.handleMessage(newBlockMsg(t, blocks[4]))
	bp.handleMessage(newBlockMsg(t, blocks[5]))
	st := bp.Stats()
	if st.Waiting != 1 || st.Queued != 1 || st.NotQueued != 1 || st.Held != 1 {
		t.Fatalf("stats %+v", st)
	}
}

// The held-back set is bounded too.
func TestBlockOrder_HeldSetBounded(t *testing.T) {
	blocks := linkedBlocks(0)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	bp.seenCap = 8
	maxHeld := 0
	for i := 0; i < 500; i++ {
		f := forkBlock(uint64(1+i%3), fmt.Sprintf("flood%d", i))
		c.failAll[f.Hash] = errors.New("refused")
		bp.handleMessage(newBlockMsg(t, f))
		if h := bp.Stats().Held; h > maxHeld {
			maxHeld = h
		}
	}
	if maxHeld > 8 {
		t.Fatalf("held set reached %d entries, bound 8", maxHeld)
	}
}

// A copy that arrives while the handler works on the block is dropped.
func TestBlockOrder_InFlightCopyDropped(t *testing.T) {
	blocks := linkedBlocks(1)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	gate := make(chan struct{})
	c.gate[blocks[1].Hash] = gate
	c.entered = make(chan string, 4)
	msg := newBlockMsg(t, blocks[1])
	first := make(chan struct{})
	go func() {
		bp.handleMessage(msg)
		close(first)
	}()
	<-c.entered
	copyDone := make(chan struct{})
	go func() {
		bp.handleMessage(msg)
		close(copyDone)
	}()
	select {
	case <-copyDone:
	case <-time.After(5 * time.Second):
		close(gate)
		t.Fatal("a copy of an in-flight block reached the handler")
	}
	close(gate)
	<-first
	expectTip(t, c, bp, 1)
	if n := c.callsFor(blocks[1]); n != 1 {
		t.Fatalf("%d handler calls, want 1", n)
	}
	if st := bp.Stats(); st.Duplicates != 1 || st.InFlight != 0 || st.Seen != 1 {
		t.Fatalf("stats %+v", st)
	}
}

// A forged copy that claims a genuine block's hash enters no state, so the
// genuine block that follows is handled (verify before mark).
func TestBlockOrder_ForgedCopyFirst(t *testing.T) {
	blocks := linkedBlocks(3)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	for _, h := range []int{1, 3} {
		forged := *blocks[h]
		forged.StateRoot = "forged"
		bp.handleMessage(newBlockMsg(t, &forged))
	}
	if st := bp.Stats(); st.Invalid != 2 || st.Seen+st.Waiting+st.Held+st.InFlight != 0 {
		t.Fatalf("forged copies entered a state: %+v", st)
	}
	bp.handleMessage(newBlockMsg(t, blocks[3]))
	bp.handleMessage(newBlockMsg(t, blocks[1]))
	bp.handleMessage(newBlockMsg(t, blocks[2]))
	expectTip(t, c, bp, 3)
}

// The tip moved by another path (HTTP chain sync): the next round tries the
// waiting blocks, including ones the other path already appended.
func TestBlockOrder_RetryAfterTipMovedElsewhere(t *testing.T) {
	blocks := linkedBlocks(6)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	bp.handleMessage(newBlockMsg(t, blocks[3]))
	bp.handleMessage(newBlockMsg(t, blocks[5]))
	bp.handleMessage(newBlockMsg(t, blocks[6]))
	c.appendDirect(blocks[1])
	c.appendDirect(blocks[2])
	bp.RetryPending()
	expectTip(t, c, bp, 3)
	c.appendDirect(blocks[4])
	c.appendDirect(blocks[5])
	bp.RetryPending()
	expectTip(t, c, bp, 6)
	if st := bp.Stats(); st.Waiting != 0 || st.FromQueue != 3 {
		t.Fatalf("stats %+v", st)
	}
	if n := c.appendedFor(blocks[5]); n != 1 {
		t.Fatalf("block 5 appended %d times", n)
	}
}

// A waiting block that is refused when tried does not stop the round: the
// next height is tried too.
func TestBlockOrder_RetrySkipsRefusedLowest(t *testing.T) {
	blocks := linkedBlocks(4)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	bp.handleMessage(newBlockMsg(t, forkBlock(3, "x")))
	bp.handleMessage(newBlockMsg(t, blocks[4]))
	if st := bp.Stats(); st.Waiting != 2 {
		t.Fatalf("stats %+v", st)
	}
	for _, h := range []int{1, 2, 3} {
		c.appendDirect(blocks[h])
	}
	bp.RetryPending()
	expectTip(t, c, bp, 4)
	if st := bp.Stats(); st.Waiting != 0 || st.HeldBack != 1 {
		t.Fatalf("stats %+v", st)
	}
}

// After a restart the propagator starts empty; the waiting block went with
// the old process and comes back with the next catch-up window.
func TestBlockOrder_RestartStartsClean(t *testing.T) {
	blocks := linkedBlocks(2)
	c := newOrderChain(blocks[0])
	before := newOrderPropagator(c)
	before.handleMessage(newBlockMsg(t, blocks[2]))
	if st := before.Stats(); st.Waiting != 1 {
		t.Fatalf("stats %+v", st)
	}
	after := newOrderPropagator(c)
	if st := after.Stats(); st != (BlockPropagationStats{}) {
		t.Fatalf("a new propagator must start empty: %+v", st)
	}
	after.handleMessage(newBlockMsg(t, blocks[1]))
	expectTip(t, c, after, 1)
	after.RetryPending()
	after.handleMessage(responseMsg(t, blocks[2]))
	expectTip(t, c, after, 2)
}

// In the producer role every external block is refused. That costs one
// handler call per block per round, and nothing waits or stays seen.
func TestBlockOrder_ProducerRoleBoundedCost(t *testing.T) {
	blocks := linkedBlocks(5)
	disabled := errors.New("external block append is disabled in the producer role")
	calls := map[string]int{}
	var mu sync.Mutex
	bp := &BlockPropagator{nodeID: "producer", seen: make(map[string]time.Time), handler: func(b *Block) error {
		mu.Lock()
		calls[b.Hash]++
		mu.Unlock()
		return disabled
	}}
	for round := 1; round <= 2; round++ {
		for copyN := 0; copyN < 10; copyN++ {
			for _, b := range blocks[1:] {
				bp.handleMessage(newBlockMsg(t, b))
			}
		}
		for _, b := range blocks[1:] {
			if calls[b.Hash] != round {
				t.Fatalf("round %d: block %d handled %d times", round, b.Height, calls[b.Hash])
			}
		}
		if st := bp.Stats(); st.Waiting != 0 || st.Seen != 0 {
			t.Fatalf("stats %+v", st)
		}
		bp.RetryPending()
	}
}

// Many peers deliver the same blocks in random orders while catch-up rounds
// run. Every block is appended exactly once and the follower reaches the top.
func TestBlockOrder_ConcurrentDeliveries(t *testing.T) {
	const top = 60
	blocks := linkedBlocks(top)
	c := newOrderChain(blocks[0])
	bp := newOrderPropagator(c)
	gossip := make([]BlockP2PMessage, top+1)
	replies := make([]BlockP2PMessage, top+1)
	for h := 1; h <= top; h++ {
		gossip[h] = newBlockMsg(t, blocks[h])
		replies[h] = responseMsg(t, blocks[h])
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				bp.RetryPending()
				time.Sleep(time.Millisecond)
			}
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			order := r.Perm(top)
			for i, idx := range order {
				if i%2 == 0 {
					bp.handleMessage(gossip[idx+1])
				} else {
					bp.handleMessage(replies[idx+1])
				}
			}
		}(int64(g + 1))
	}
	wg.Wait()
	close(stop)
	bp.RetryPending()
	bp.handleMessage(responseMsg(t, blocks[1:]...))
	expectTip(t, c, bp, top)
	for _, b := range blocks[1:] {
		if n := c.appendedFor(b); n != 1 {
			t.Fatalf("block %d appended %d times", b.Height, n)
		}
	}
	if st := bp.Stats(); st.InFlight != 0 || st.Waiting != 0 {
		t.Fatalf("stats %+v", st)
	}
}

// TryAppendExternalBlock marks a gap with ErrExternalBlockAhead and the tip;
// a block at the next height that does not link to the tip is still refused
// without it. The block is refused in both cases, as before.
func TestTryAppendExternalBlock_AheadOfTip(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	defer pool.Stop()
	source := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	var produced []*Block
	for i := 0; i < 3; i++ {
		if err := pool.Add(makeTx(fmt.Sprintf("ahead-%d", i), 0.5)); err != nil {
			t.Fatal(err)
		}
		b, err := source.ProduceBlock()
		if err != nil {
			t.Fatal(err)
		}
		produced = append(produced, b)
	}
	targetPool := mempool.New(mempool.DefaultConfig())
	defer targetPool.Stop()
	target := NewBlockProducer(targetPool, newTestApplier(), DefaultProducerConfig())
	if err := target.TryAppendExternalBlock(produced[0]); err != nil {
		t.Fatal(err)
	}
	err := target.TryAppendExternalBlock(produced[2])
	var ahead *ExternalBlockAheadError
	if !errors.Is(err, ErrExternalBlockAhead) || !errors.As(err, &ahead) || ahead.Height != 2 || ahead.Tip != 0 {
		t.Fatalf("block 2 at tip 0: got %v", err)
	}
	if target.TipHeight() != 0 {
		t.Fatalf("an ahead block must not be appended")
	}
	fork := *produced[1]
	fork.PrevHash = "not-the-tip"
	fork.Hash = computeBlockHash(&fork)
	if err := target.TryAppendExternalBlock(&fork); err == nil || errors.Is(err, ErrExternalBlockAhead) {
		t.Fatalf("a non-linking block at the next height: got %v", err)
	}
	if err := target.TryAppendExternalBlock(produced[1]); err != nil {
		t.Fatal(err)
	}
	if err := target.TryAppendExternalBlock(produced[2]); err != nil {
		t.Fatal(err)
	}
	if target.TipHeight() != 2 {
		t.Fatalf("tip %d, want 2", target.TipHeight())
	}
}
