package chain

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// FuzzBlockResponseBudget drives the block_request response budget with a
// sequence of requests the way they arrive: any requester (the pubsub
// author) and any spacing in time. Each op is 3 bytes: the requester (one of
// 9, so the 4-entry table used here fills up and gets swept) and a little
// endian clock step in milliseconds. The clock never goes backwards, as
// time.Now's monotonic reading does not. Whatever the sequence:
//   - every request is either answered or counted as dropped;
//   - the per-peer table never holds more than its bound;
//   - all peers together get at most the global burst plus one answer per
//     global interval that has passed;
//   - one peer gets at most its burst plus one answer per peer interval
//     that has passed since its first request.
func FuzzBlockResponseBudget(f *testing.F) {
	op := func(peer byte, stepMS uint16) []byte {
		b := []byte{peer, 0, 0}
		binary.LittleEndian.PutUint16(b[1:], stepMS)
		return b
	}
	seq := func(ops ...[]byte) []byte {
		var out []byte
		for _, o := range ops {
			out = append(out, o...)
		}
		return out
	}
	f.Add([]byte{})
	f.Add(seq(op(0, 0), op(0, 0), op(0, 0), op(0, 0), op(0, 0), op(0, 2000), op(0, 0)))
	f.Add(seq(op(0, 0), op(1, 0), op(2, 0), op(3, 0), op(4, 0), op(5, 0), op(6, 0), op(7, 0), op(8, 0), op(8, 500)))
	f.Add(seq(op(1, 0), op(2, 0), op(3, 0), op(4, 0), op(5, 9000), op(6, 0), op(7, 1000), op(1, 0)))
	f.Add(seq(op(3, 65535), op(3, 1), op(3, 1), op(3, 1), op(3, 1), op(3, 1999), op(3, 1)))
	f.Add(seq(op(0, 0), op(0, 0), op(0, 0), op(1, 0), op(1, 0), op(1, 0), op(2, 0), op(2, 0), op(2, 0), op(3, 0), op(3, 0), op(3, 0)))

	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 3*4096 {
			ops = ops[:3*4096]
		}
		now := time.Unix(1_800_000_000, 0)
		rb := newResponseBudget()
		rb.maxPeers = 4
		rb.now = func() time.Time { return now }

		start := now
		first := map[string]time.Time{}
		granted := map[string]int{}
		total, calls := 0, 0
		for i := 0; i+3 <= len(ops); i += 3 {
			peer := fmt.Sprintf("peer-%d", ops[i]%9)
			now = now.Add(time.Duration(binary.LittleEndian.Uint16(ops[i+1:])) * time.Millisecond)
			if _, ok := first[peer]; !ok {
				first[peer] = now
			}
			calls++
			if rb.allow(peer) {
				granted[peer]++
				total++
			}
			if n := len(rb.peers); n > rb.maxPeers {
				t.Fatalf("op %d: the peer table holds %d buckets, bound %d", i/3, n, rb.maxPeers)
			}
			globalCap := rb.globalBurst + float64(now.Sub(start))/float64(rb.globalEvery)
			if float64(total) > globalCap+1e-9 {
				t.Fatalf("op %d: %d answers in %s, the global budget allows %.3f", i/3, total, now.Sub(start), globalCap)
			}
			peerCap := rb.peerBurst + float64(now.Sub(first[peer]))/float64(rb.peerEvery)
			if float64(granted[peer]) > peerCap+1e-9 {
				t.Fatalf("op %d: %s got %d answers in %s, its budget allows %.3f",
					i/3, peer, granted[peer], now.Sub(first[peer]), peerCap)
			}
		}
		if got := rb.droppedCount(); got != uint64(calls-total) {
			t.Fatalf("%d requests, %d answered, but %d counted as dropped", calls, total, got)
		}
	})
}

var (
	fuzzBlockTopicOnce sync.Once
	fuzzBlockTopic     *pubsub.Topic
	fuzzBlockTopicErr  error
)

// fuzzBlockTopicForTest joins the block topic once per process, on a host
// with no peers, so a propagator under fuzzing can publish its responses.
// The short seen-message TTL keeps pubsub's duplicate cache small however
// many responses the fuzzer makes it publish.
func fuzzBlockTopicForTest(tb testing.TB) *pubsub.Topic {
	fuzzBlockTopicOnce.Do(func() {
		h, err := libp2p.New(libp2p.NoListenAddrs)
		if err != nil {
			fuzzBlockTopicErr = err
			return
		}
		ps, err := pubsub.NewGossipSub(context.Background(), h, pubsub.WithSeenMessagesTTL(5*time.Second))
		if err != nil {
			fuzzBlockTopicErr = err
			return
		}
		fuzzBlockTopic, fuzzBlockTopicErr = ps.Join(BlockTopicName)
	})
	if fuzzBlockTopicErr != nil {
		tb.Fatal(fuzzBlockTopicErr)
	}
	return fuzzBlockTopic
}

// FuzzBlockPropagatorMessage feeds one qsdm-blocks message to the block
// propagator the way its read loop does (decode, then handleMessageFrom with
// the authenticated pubsub author), repeated up to 15 times by up to three
// authors at the same instant. Whatever the message:
//   - the block store is read only for requests within the budget: at most
//     the per-peer burst for each author, and never more than the global
//     burst in all;
//   - the block handler sees only blocks whose hash and producer signature
//     check out, and each hash at most once; the seen set holds exactly the
//     hashes handled.
func FuzzBlockPropagatorMessage(f *testing.F) {
	prev := log.Writer()
	log.SetOutput(io.Discard) // the propagator logs every rejected block
	f.Cleanup(func() { log.SetOutput(prev) })

	genuine := makeBlock(9)
	genuine.Hash = computeBlockHash(genuine)
	forged := makeBlock(9)
	forged.StateRoot = "forged"
	forged.Hash = genuine.Hash
	env := func(kind string, payload any, target string) []byte {
		p, err := json.Marshal(payload)
		if err != nil {
			f.Fatal(err)
		}
		raw, err := json.Marshal(BlockP2PMessage{Kind: kind, Payload: p, OriginNode: "remote", TargetNode: target,
			RequestID: "20260801T120000.000000000Z", Timestamp: "2026-08-01T12:00:00Z"})
		if err != nil {
			f.Fatal(err)
		}
		return raw
	}
	f.Add(env(BlockMessageRequest, BlockRequest{From: 3, To: 3, Limit: 1}, ""), []byte{0}, uint8(15))
	f.Add(env(BlockMessageRequest, BlockRequest{From: 100, To: 163, Limit: 64}, ""), []byte{0, 1, 2}, uint8(15))
	f.Add(env(BlockMessageRequest, BlockRequest{From: 3, To: 3}, "someone-else"), []byte{0}, uint8(3))
	f.Add(env(BlockMessageNewBlock, genuine, ""), []byte{1}, uint8(4))
	f.Add(env(BlockMessageNewBlock, forged, ""), []byte{1}, uint8(2))
	f.Add(env(BlockMessageResponse, BlockResponse{From: 9, To: 9, Blocks: []*Block{forged, genuine, genuine}}, ""), []byte{2}, uint8(2))
	f.Add([]byte(`{"kind":"block_request","payload":null}`), []byte{0, 1}, uint8(9))

	topic := fuzzBlockTopicForTest(f)
	f.Fuzz(func(t *testing.T, raw []byte, authors []byte, repeats uint8) {
		if len(raw) > 64<<10 {
			return // keeps each run short; larger messages take the same paths
		}
		var msg BlockP2PMessage
		if json.Unmarshal(raw, &msg) != nil {
			return
		}
		if len(authors) == 0 {
			authors = []byte{0}
		}
		clock := time.Unix(1_800_000_000, 0)
		budget := newResponseBudget()
		budget.now = func() time.Time { return clock }
		reads := 0
		handled := map[string]int{}
		bp := &BlockPropagator{
			nodeID: "local",
			topic:  topic,
			ctx:    context.Background(),
			budget: budget,
			seen:   make(map[string]time.Time),
			provider: func(from, to uint64, limit int) []*Block {
				reads++
				if from%2 == 0 {
					return nil // nothing to send, so no response is published
				}
				return []*Block{genuine}
			},
			handler: func(b *Block) error {
				if recomputeHash(b) != b.Hash || VerifyBlockSignature(b) != nil {
					t.Fatalf("the handler got a block that does not verify: height %d hash %q", b.Height, b.Hash)
				}
				handled[b.Hash]++
				return nil
			},
		}
		perAuthor := map[string]int{}
		n := int(repeats % 16)
		for i := 0; i < n; i++ {
			author := fmt.Sprintf("author-%d", authors[i%len(authors)]%3)
			perAuthor[author]++
			bp.handleMessageFrom(msg, author)
		}
		maxReads := 0
		for _, c := range perAuthor {
			if c > blockResponsePeerBurst {
				c = blockResponsePeerBurst
			}
			maxReads += c
		}
		if maxReads > blockResponseGlobalBurst {
			maxReads = blockResponseGlobalBurst
		}
		if reads > maxReads {
			t.Fatalf("%d block store reads for %d requests by %v; the budget allows %d", reads, n, perAuthor, maxReads)
		}
		for h, c := range handled {
			if c != 1 {
				t.Fatalf("block %q handled %d times", h, c)
			}
		}
		if len(bp.seen) != len(handled) {
			t.Fatalf("the seen set holds %d hashes, but %d blocks were handled", len(bp.seen), len(handled))
		}
	})
}
