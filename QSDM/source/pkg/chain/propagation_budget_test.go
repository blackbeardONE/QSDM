package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

type budgetClock struct{ t time.Time }

func (c *budgetClock) now() time.Time           { return c.t }
func (c *budgetClock) add(d time.Duration)      { c.t = c.t.Add(d) }
func newBudgetClock() *budgetClock              { return &budgetClock{t: time.Unix(1_800_000_000, 0)} }
func testBudget(c *budgetClock) *responseBudget { rb := newResponseBudget(); rb.now = c.now; return rb }

func TestResponseBudget_PerPeerBurstThenRefill(t *testing.T) {
	clock := newBudgetClock()
	rb := testBudget(clock)
	for i := 0; i < blockResponsePeerBurst; i++ {
		if !rb.allow("peer-a") {
			t.Fatalf("request %d within the burst was refused", i+1)
		}
	}
	if rb.allow("peer-a") {
		t.Fatal("request past the burst was answered")
	}
	clock.add(blockResponsePeerEvery / 2)
	if rb.allow("peer-a") {
		t.Fatal("half a refill interval must not earn a token")
	}
	clock.add(blockResponsePeerEvery / 2)
	if !rb.allow("peer-a") {
		t.Fatal("a full refill interval must earn one token")
	}
	if rb.allow("peer-a") {
		t.Fatal("only one token was earned")
	}
	if got := rb.droppedCount(); got != 3 {
		t.Fatalf("dropped = %d, want 3", got)
	}
}

func TestResponseBudget_GlobalCapAcrossPeers(t *testing.T) {
	clock := newBudgetClock()
	rb := testBudget(clock)
	for i := 0; i < blockResponseGlobalBurst; i++ {
		if !rb.allow(fmt.Sprintf("peer-%d", i)) {
			t.Fatalf("peer %d within the global burst was refused", i)
		}
	}
	if rb.allow("peer-new") {
		t.Fatal("a new peer was answered with the global budget spent")
	}
	if _, tracked := rb.peers["peer-new"]; tracked {
		t.Fatal("a peer refused by the global budget must not get a bucket")
	}
	clock.add(blockResponseGlobalEvery)
	if !rb.allow("peer-new") {
		t.Fatal("one global refill interval must earn one token")
	}
	if rb.allow("peer-other") {
		t.Fatal("only one global token was earned")
	}
}

func TestResponseBudget_RefusedRequestSpendsNothing(t *testing.T) {
	clock := newBudgetClock()
	rb := testBudget(clock)
	for i := 0; i < blockResponsePeerBurst; i++ {
		rb.allow("greedy")
	}
	for i := 0; i < 50; i++ {
		if rb.allow("greedy") {
			t.Fatal("greedy peer answered past its burst")
		}
	}
	// The greedy peer's refused requests left the rest of the global burst
	// for everyone else.
	for i := 0; i < blockResponseGlobalBurst-blockResponsePeerBurst; i++ {
		if !rb.allow(fmt.Sprintf("other-%d", i)) {
			t.Fatalf("other peer %d refused: refused requests spent global tokens", i)
		}
	}
	if rb.allow("one-too-many") {
		t.Fatal("global burst exceeded")
	}
}

func TestResponseBudget_PeerTableBoundAndSweep(t *testing.T) {
	clock := newBudgetClock()
	rb := testBudget(clock)
	rb.maxPeers = 3
	rb.globalBurst = 100
	for _, p := range []string{"a", "b", "c"} {
		if !rb.allow(p) {
			t.Fatalf("peer %s refused", p)
		}
	}
	if rb.allow("d") {
		t.Fatal("a peer was added to a full table with no idle bucket")
	}
	if !rb.allow("a") {
		t.Fatal("a peer already in the table must still be served")
	}
	// After a full refill interval the idle buckets can go.
	clock.add(time.Duration(blockResponsePeerBurst) * blockResponsePeerEvery)
	if !rb.allow("d") {
		t.Fatal("idle buckets were not swept for a new peer")
	}
	if len(rb.peers) > rb.maxPeers {
		t.Fatalf("table holds %d peers, bound %d", len(rb.peers), rb.maxPeers)
	}
}

func TestResponseBudget_ClockStepBackEarnsNothing(t *testing.T) {
	clock := newBudgetClock()
	rb := testBudget(clock)
	for i := 0; i < blockResponsePeerBurst; i++ {
		rb.allow("peer-a")
	}
	clock.add(-time.Hour)
	if rb.allow("peer-a") {
		t.Fatal("a clock step backwards earned tokens")
	}
	clock.add(time.Hour + blockResponsePeerEvery)
	if !rb.allow("peer-a") {
		t.Fatal("refill must resume from the last good time")
	}
}

// countResponses reads block_response messages from sub. It waits up to 3 s
// for each of the first want, then 300 ms for any extra one.
func countResponses(t *testing.T, sub *pubsub.Subscription, want int) int {
	t.Helper()
	got := 0
	for {
		wait := 3 * time.Second
		if got >= want {
			wait = 300 * time.Millisecond
		}
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		m, err := sub.Next(ctx)
		cancel()
		if err != nil {
			return got
		}
		var env BlockP2PMessage
		if json.Unmarshal(m.Data, &env) == nil && env.Kind == BlockMessageResponse {
			got++
		}
	}
}

func budgetTestPropagator(t *testing.T, provider BlockProvider) (*BlockPropagator, *pubsub.Subscription) {
	t.Helper()
	_, topic, sub := setupPropagationTest(t)
	bp := &BlockPropagator{
		nodeID:   "local",
		topic:    topic,
		ctx:      context.Background(),
		provider: provider,
		budget:   testBudget(newBudgetClock()),
		seen:     make(map[string]time.Time),
	}
	return bp, sub
}

func blockRequestMsg(t *testing.T, from, to uint64) BlockP2PMessage {
	t.Helper()
	payload, err := json.Marshal(BlockRequest{From: from, To: to, Limit: to - from + 1})
	if err != nil {
		t.Fatal(err)
	}
	return BlockP2PMessage{Kind: BlockMessageRequest, Payload: payload, OriginNode: "remote"}
}

func TestBlockPropagator_BlockRequestsOverBudgetGetNoReply(t *testing.T) {
	block := makeBlock(3)
	block.Hash = computeBlockHash(block)
	var mu sync.Mutex
	calls := 0
	bp, sub := budgetTestPropagator(t, func(from, to uint64, limit int) []*Block {
		mu.Lock()
		calls++
		mu.Unlock()
		return []*Block{block}
	})

	const extra = 3
	for i := 0; i < blockResponsePeerBurst+extra; i++ {
		bp.handleMessageFrom(blockRequestMsg(t, 3, 3), "peer-a")
	}
	if got := countResponses(t, sub, blockResponsePeerBurst); got != blockResponsePeerBurst {
		t.Fatalf("published %d responses, want %d", got, blockResponsePeerBurst)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != blockResponsePeerBurst {
		t.Fatalf("provider called %d times, want %d: over-budget requests must not read the store", calls, blockResponsePeerBurst)
	}
	if got := bp.DroppedBlockResponses(); got != extra {
		t.Fatalf("DroppedBlockResponses = %d, want %d", got, extra)
	}
}

func TestBlockPropagator_EmptyBlockResponseNotPublished(t *testing.T) {
	calls := 0
	bp, sub := budgetTestPropagator(t, func(from, to uint64, limit int) []*Block {
		calls++
		return nil
	})
	bp.handleMessageFrom(blockRequestMsg(t, 100, 163), "peer-a")
	if got := countResponses(t, sub, 0); got != 0 {
		t.Fatalf("published %d responses for a request above the tip, want 0", got)
	}
	if calls != 1 {
		t.Fatalf("provider called %d times, want 1", calls)
	}
}

func TestBlockPropagator_ForgedBlockCannotTakeGenuineSlot(t *testing.T) {
	var handled []string
	bp := &BlockPropagator{
		seen: make(map[string]time.Time),
		handler: func(b *Block) error {
			handled = append(handled, b.StateRoot)
			return nil
		},
	}
	genuine := makeBlock(9)
	genuine.Hash = computeBlockHash(genuine)
	forged := makeBlock(9)
	forged.StateRoot = "forged"
	forged.Hash = genuine.Hash // claims the genuine hash; the content does not match it

	bp.handleBlock(forged, "attacker")
	bp.handleBlock(genuine, "honest")
	bp.handleBlock(forged, "attacker")
	bp.handleBlock(genuine, "honest")
	if len(handled) != 1 || handled[0] != genuine.StateRoot {
		t.Fatalf("handled %v, want the genuine block once", handled)
	}
}

func TestBlockPropagator_SeenSetEvictsOldestPastCap(t *testing.T) {
	bp := &BlockPropagator{seen: make(map[string]time.Time), seenCap: 3}
	for _, h := range []string{"h1", "h2", "h3", "h4"} {
		if !bp.markSeen(h) {
			t.Fatalf("%s reported as already seen", h)
		}
	}
	if got := bp.SeenCount(); got != 3 {
		t.Fatalf("SeenCount = %d, want 3", got)
	}
	if bp.markSeen("h4") {
		t.Fatal("h4 is still in the set")
	}
	if !bp.markSeen("h1") {
		t.Fatal("h1 should have been evicted first")
	}
	if got := bp.SeenCount(); got != 3 {
		t.Fatalf("SeenCount = %d after re-adding h1, want 3", got)
	}
}
