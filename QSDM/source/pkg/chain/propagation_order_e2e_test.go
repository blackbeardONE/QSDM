package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// End to end: a producer and a P2P-only follower on real gossipsub, with
// blocks sealed and signed by the producer, an allowlisted producer on the
// follower, and the follower appending through TryAppendExternalBlock.

type pubsubJoiner struct{ ps *pubsub.PubSub }

func (j pubsubJoiner) JoinTopic(name string) (*pubsub.Topic, *pubsub.Subscription, error) {
	topic, err := j.ps.Join(name)
	if err != nil {
		return nil, nil, err
	}
	sub, err := topic.Subscribe()
	if err != nil {
		return nil, nil, err
	}
	return topic, sub, nil
}

type orderNode struct {
	host  host.Host
	bp    *BlockPropagator
	calls atomic.Int64
}

func newOrderNode(t *testing.T, handler BlockHandler) *orderNode {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		cancel()
		h.Close()
		t.Fatalf("gossipsub: %v", err)
	}
	n := &orderNode{host: h}
	counted := func(b *Block) error {
		n.calls.Add(1)
		if handler == nil {
			return nil
		}
		return handler(b)
	}
	bp, err := NewBlockPropagator(pubsubJoiner{ps}, h.ID().String(), counted)
	if err != nil {
		cancel()
		h.Close()
		t.Fatalf("propagator: %v", err)
	}
	n.bp = bp
	t.Cleanup(func() {
		bp.Close()
		cancel()
		h.Close()
	})
	return n
}

func (n *orderNode) stop() {
	n.bp.Close()
	n.host.Close()
}

func topicHasPeer(bp *BlockPropagator, id peer.ID) bool {
	for _, p := range bp.topic.ListPeers() {
		if p == id {
			return true
		}
	}
	return false
}

func connectOrderNodes(t *testing.T, a, b *orderNode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.host.Connect(ctx, peer.AddrInfo{ID: b.host.ID(), Addrs: b.host.Addrs()}); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if !eventually(10*time.Second, func() bool {
		return topicHasPeer(a.bp, b.host.ID()) && topicHasPeer(b.bp, a.host.ID())
	}) {
		t.Fatal("the two nodes never saw each other on the block topic")
	}
	// Gossipsub drops a message sent before the receiver has registered the
	// sender, so the ordered sends start only once a probe got through each
	// way. The propagators ignore the probe kind.
	warmUpOrderPath(t, a, b)
	warmUpOrderPath(t, b, a)
}

func warmUpOrderPath(t *testing.T, from, to *orderNode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sub, err := to.bp.topic.Subscribe()
	if err != nil {
		t.Fatalf("probe subscription: %v", err)
	}
	defer sub.Cancel()
	got := make(chan struct{})
	go func() {
		for {
			m, err := sub.Next(ctx)
			if err != nil {
				return
			}
			if m.GetFrom() == from.host.ID() {
				close(got)
				return
			}
		}
	}()
	probe, err := json.Marshal(BlockP2PMessage{Kind: "probe", OriginNode: from.host.ID().String()})
	if err != nil {
		t.Fatal(err)
	}
	for {
		if err := from.bp.topic.Publish(ctx, probe); err != nil && ctx.Err() == nil {
			t.Fatalf("probe publish: %v", err)
		}
		select {
		case <-got:
			return
		case <-ctx.Done():
			t.Fatal("no probe got through between the two nodes")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func eventually(limit time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// signedOrderChain seals blocks 0..n on a producer that signs with ML-DSA.
func signedOrderChain(t *testing.T, n int) ([]*Block, string) {
	t.Helper()
	signer, _ := newBFTKey(t)
	pool := mempool.New(mempool.DefaultConfig())
	t.Cleanup(pool.Stop)
	producer := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	producer.SetBlockSigner(signer)
	blocks := make([]*Block, 0, n+1)
	for i := 0; i <= n; i++ {
		if err := pool.Add(makeTx(fmt.Sprintf("order-e2e-%d", i), 0.01)); err != nil {
			t.Fatal(err)
		}
		b, err := producer.ProduceBlock()
		if err != nil {
			t.Fatalf("seal %d: %v", i, err)
		}
		if !b.ProducerAuth.Signed() {
			t.Fatalf("block %d is not signed", i)
		}
		blocks = append(blocks, b)
	}
	return blocks, blocks[0].ProducerID
}

// newOrderFollower is a follower that pins the producer and holds the given
// blocks, as one that reloaded them from its journal.
func newOrderFollower(t *testing.T, producerID string, have []*Block) *BlockProducer {
	t.Helper()
	pool := mempool.New(mempool.DefaultConfig())
	t.Cleanup(pool.Stop)
	f := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	f.SetAuthorizedBlockProducers([]string{producerID})
	for _, b := range have {
		if err := f.TryAppendExternalBlock(b); err != nil {
			t.Fatalf("follower load %d: %v", b.Height, err)
		}
	}
	return f
}

func orderProvider(blocks []*Block) BlockProvider {
	return func(from, to uint64, limit int) []*Block {
		var out []*Block
		for h := from; h <= to && h < uint64(len(blocks)) && len(out) < limit; h++ {
			out = append(out, blocks[h])
		}
		return out
	}
}

var errOrderProducerRole = errors.New("external block append is disabled in the producer role")

func newOrderProducerNode(t *testing.T, blocks []*Block) *orderNode {
	t.Helper()
	n := newOrderNode(t, func(*Block) error { return errOrderProducerRole })
	n.bp.SetBlockProvider(orderProvider(blocks))
	return n
}

func requireTip(t *testing.T, f *BlockProducer, n *orderNode, want uint64) {
	t.Helper()
	if !eventually(10*time.Second, func() bool { return f.TipHeight() >= want }) || f.TipHeight() != want {
		t.Fatalf("follower tip %d, want %d (handler calls %d, stats %+v)", f.TipHeight(), want, n.calls.Load(), n.bp.Stats())
	}
}

// The reported case on the wire: 2, 1, 2 from an allowlisted producer, then
// a block_response replay. The follower reaches tip 2; the replay is a
// duplicate and appends nothing twice.
func TestBlockOrderE2E_Reporter212AndResponseReplay(t *testing.T) {
	blocks, producerID := signedOrderChain(t, 2)
	follower := newOrderFollower(t, producerID, blocks[:1])
	prod := newOrderProducerNode(t, blocks)
	fol := newOrderNode(t, follower.TryAppendExternalBlock)
	connectOrderNodes(t, prod, fol)

	if err := prod.bp.BroadcastBlock(blocks[2]); err != nil {
		t.Fatal(err)
	}
	eventually(10*time.Second, func() bool { return fol.calls.Load() >= 1 })
	if err := prod.bp.BroadcastBlock(blocks[1]); err != nil {
		t.Fatal(err)
	}
	eventually(10*time.Second, func() bool { return follower.TipHeight() >= 2 })
	if err := prod.bp.BroadcastBlock(blocks[2]); err != nil {
		t.Fatal(err)
	}
	eventually(5*time.Second, func() bool { return fol.bp.Stats().Duplicates >= 1 })
	requireTip(t, follower, fol, 2)

	callsBefore := fol.calls.Load()
	fol.bp.RetryPending()
	if err := fol.bp.RequestBlocks(1, 2, 64); err != nil {
		t.Fatal(err)
	}
	if !eventually(10*time.Second, func() bool { return fol.bp.Stats().Duplicates >= 3 }) {
		t.Fatalf("the block_response replay never arrived: %+v", fol.bp.Stats())
	}
	if fol.calls.Load() != callsBefore {
		t.Fatalf("replayed blocks reached the handler again")
	}
	requireTip(t, follower, fol, 2)
	if st := fol.bp.Stats(); st.FromQueue != 1 || st.Waiting != 0 || st.InFlight != 0 {
		t.Fatalf("stats %+v", st)
	}
}

// The replay path on its own: block 2 cannot wait (a one-block window), so it
// is held back; the next catch-up round's block_response delivers it.
func TestBlockOrderE2E_ResponseReplayDeliversRefusedBlock(t *testing.T) {
	blocks, producerID := signedOrderChain(t, 2)
	follower := newOrderFollower(t, producerID, blocks[:1])
	prod := newOrderProducerNode(t, blocks)
	fol := newOrderNode(t, follower.TryAppendExternalBlock)
	fol.bp.SetMaxResponseBlocks(1)
	connectOrderNodes(t, prod, fol)

	if err := prod.bp.BroadcastBlock(blocks[2]); err != nil {
		t.Fatal(err)
	}
	eventually(10*time.Second, func() bool { return fol.calls.Load() >= 1 })
	if err := prod.bp.BroadcastBlock(blocks[1]); err != nil {
		t.Fatal(err)
	}
	requireTip(t, follower, fol, 1)

	fol.bp.RetryPending() // the next catch-up round
	if err := fol.bp.RequestBlocks(2, 2, 1); err != nil {
		t.Fatal(err)
	}
	requireTip(t, follower, fol, 2)
}

// A restart drops the in-memory queue; the restarted follower starts from its
// journal and catches up with gossip and the next catch-up window.
func TestBlockOrderE2E_Restart(t *testing.T) {
	blocks, producerID := signedOrderChain(t, 3)
	prod := newOrderProducerNode(t, blocks)

	first := newOrderFollower(t, producerID, blocks[:1])
	fol1 := newOrderNode(t, first.TryAppendExternalBlock)
	connectOrderNodes(t, prod, fol1)
	for _, h := range []int{2, 3} {
		if err := prod.bp.BroadcastBlock(blocks[h]); err != nil {
			t.Fatal(err)
		}
	}
	if !eventually(10*time.Second, func() bool { return fol1.bp.Stats().Waiting == 2 }) {
		t.Fatalf("blocks 2 and 3 should wait: %+v", fol1.bp.Stats())
	}
	fol1.stop()

	second := newOrderFollower(t, producerID, blocks[:first.TipHeight()+1])
	fol2 := newOrderNode(t, second.TryAppendExternalBlock)
	if st := fol2.bp.Stats(); st != (BlockPropagationStats{}) {
		t.Fatalf("a restarted propagator must start empty: %+v", st)
	}
	connectOrderNodes(t, prod, fol2)
	if err := prod.bp.BroadcastBlock(blocks[1]); err != nil {
		t.Fatal(err)
	}
	requireTip(t, second, fol2, 1)
	fol2.bp.RetryPending()
	if err := fol2.bp.RequestBlocks(2, 65, 64); err != nil {
		t.Fatal(err)
	}
	requireTip(t, second, fol2, 3)
}

// A flood of genuine blocks ahead of the tip over the wire: the queue stays
// within one window, and the follower still catches up.
func TestBlockOrderE2E_FloodStaysBounded(t *testing.T) {
	const top = 100
	blocks, producerID := signedOrderChain(t, top)
	follower := newOrderFollower(t, producerID, blocks[:1])
	prod := newOrderProducerNode(t, blocks)
	fol := newOrderNode(t, follower.TryAppendExternalBlock)
	connectOrderNodes(t, prod, fol)

	var maxWaiting atomic.Int64
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			if w := int64(fol.bp.Stats().Waiting); w > maxWaiting.Load() {
				maxWaiting.Store(w)
			}
			select {
			case <-done:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	// One at a time, so gossipsub's outbound queue (32 messages) never drops
	// one: each distinct block costs the follower exactly one handler call.
	for h := top; h >= 2; h-- {
		if err := prod.bp.BroadcastBlock(blocks[h]); err != nil {
			t.Fatal(err)
		}
		want := int64(top - h + 1)
		eventually(5*time.Second, func() bool { return fol.calls.Load() >= want })
	}
	close(done)
	<-sampled
	if w := maxWaiting.Load(); w > defaultBlockResponseLimit {
		t.Fatalf("queue held %d blocks, bound %d", w, defaultBlockResponseLimit)
	}
	if err := prod.bp.BroadcastBlock(blocks[1]); err != nil {
		t.Fatal(err)
	}
	requireTip(t, follower, fol, 64)
	fol.bp.RetryPending()
	if err := fol.bp.RequestBlocks(65, 128, 64); err != nil {
		t.Fatal(err)
	}
	requireTip(t, follower, fol, top)
}
