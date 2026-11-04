package networking

import (
	"encoding/binary"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	pb "github.com/libp2p/go-libp2p-pubsub/pb"
	"github.com/libp2p/go-libp2p/core/peer"
)

// fuzzGuardTopics are the topics a fuzzed message can claim: the six QSDM
// topics, one the guard has no limit for, and the empty name.
var fuzzGuardTopics = []string{
	TxTopicName, BFTTopicName, blockTopicNameForLimits, EvidenceTopicName, PolTopicName,
	bridgeTopicNameForLimits, "qsdm-unknown", "",
}

// FuzzGossipGuardValidate drives the gossip guard, the default GossipSub
// validator on every topic when the P2P limits are on, with a sequence of
// messages the way they arrive from peers: any topic, any size, any
// forwarding peer, any spacing in time. rate and burst pick the configured
// limits (0 means the default; a rate above 200 means no rate limit, as a
// negative gossip_peer_rate does). Each op is 5 bytes: the forwarding peer
// (one of 5; 0 is this node), the topic, the payload size in 64-byte units
// (little endian), and the clock step (n x 10 ms, or (n-249) x 5 minutes
// from 250 up, so idle buckets get removed). The clock never goes
// backwards, as time.Now's monotonic reading does not. Whatever the
// sequence:
//   - this node's own messages are always accepted;
//   - a message from another peer above its topic's limit is rejected, and
//     one within the limit never is;
//   - every message from another peer is counted exactly once;
//   - no (topic, peer) gets more messages accepted than the burst of each
//     bucket the guard may have created for it (one, plus one per idle
//     period) plus the rate earned over the time that passed.
func FuzzGossipGuardValidate(f *testing.F) {
	op := func(p, topic byte, units uint16, step byte) []byte {
		b := []byte{p, topic, 0, 0, step}
		binary.LittleEndian.PutUint16(b[2:], units)
		return b
	}
	seq := func(ops ...[]byte) []byte {
		var out []byte
		for _, o := range ops {
			out = append(out, o...)
		}
		return out
	}
	f.Add(uint8(0), uint16(0), []byte{})
	f.Add(uint8(2), uint16(3), seq(op(1, 0, 1, 0), op(1, 0, 1, 0), op(1, 0, 1, 0), op(1, 0, 1, 0), op(1, 0, 1, 100), op(1, 0, 1, 0)))
	f.Add(uint8(0), uint16(0), seq(op(1, 5, 1024, 0), op(1, 5, 1025, 0), op(0, 5, 16000, 0), op(2, 6, 16384, 0), op(2, 6, 16385, 0)))
	f.Add(uint8(1), uint16(1), seq(op(3, 2, 0, 251), op(3, 2, 0, 0), op(3, 7, 4096, 255), op(4, 1, 65535, 0)))
	f.Add(uint8(250), uint16(1), seq(op(1, 0, 1, 0), op(1, 0, 1, 0), op(1, 0, 1, 0)))

	// The guard reads only the payload length, so every message shares one
	// buffer instead of allocating up to 4 MiB per op.
	payload := make([]byte, 65535*64)
	f.Fuzz(func(t *testing.T, rate uint8, burst uint16, ops []byte) {
		if len(ops) > 5*2048 {
			ops = ops[:5*2048]
		}
		self := peer.ID("self")
		peers := []peer.ID{self, "peer-a", "peer-b", "peer-c", "peer-d"}
		lim := P2PLimits{GossipPeerRate: float64(rate), GossipPeerBurst: int(burst)}
		if rate > 200 {
			lim.GossipPeerRate = -1
		}
		lim = lim.WithDefaults()
		g := newGossipGuard(self, lim)
		now := time.Unix(1_800_000_000, 0)
		g.now = func() time.Time { return now }

		type key struct {
			topic string
			p     peer.ID
		}
		first := map[key]time.Time{}
		accepted := map[key]int{}
		remote := 0
		for i := 0; i+5 <= len(ops); i += 5 {
			from := peers[int(ops[i])%len(peers)]
			topic := fuzzGuardTopics[int(ops[i+1])%len(fuzzGuardTopics)]
			size := int(binary.LittleEndian.Uint16(ops[i+2:])) * 64
			step := time.Duration(ops[i+4]) * 10 * time.Millisecond
			if ops[i+4] >= 250 {
				step = time.Duration(ops[i+4]-249) * 5 * time.Minute
			}
			now = now.Add(step)

			topicName := topic
			r := g.validate(nil, from, &pubsub.Message{Message: &pb.Message{Topic: &topicName, Data: payload[:size]}})
			if from == self {
				if r != pubsub.ValidationAccept {
					t.Fatalf("op %d: own message on %q (%d bytes) got %v", i/5, topic, size, r)
				}
				continue
			}
			remote++
			max, ok := lim.GossipTopicMaxBytes[topic]
			if !ok {
				max = lim.GossipMaxMessageBytes
			}
			if size > max {
				if r != pubsub.ValidationReject {
					t.Fatalf("op %d: %d bytes on %q (limit %d) got %v, want reject", i/5, size, topic, max, r)
				}
				continue
			}
			if r == pubsub.ValidationReject {
				t.Fatalf("op %d: %d bytes on %q (limit %d) rejected", i/5, size, topic, max)
			}
			k := key{topic, from}
			if _, seen := first[k]; !seen {
				first[k] = now
			}
			if r != pubsub.ValidationAccept {
				continue
			}
			accepted[k]++
			if lim.GossipPeerRate <= 0 {
				continue
			}
			span := now.Sub(first[k])
			buckets := 1 + int(span/gossipGuardIdleTTL)
			allowed := float64(buckets)*g.burst + lim.GossipPeerRate*span.Seconds()
			if float64(accepted[k]) > allowed+1e-6 {
				t.Fatalf("op %d: %s on %q: %d accepted in %s, at most %.3f allowed (rate %v, burst %v)",
					i/5, from, topic, accepted[k], span, allowed, lim.GossipPeerRate, g.burst)
			}
		}
		st := g.Stats()
		if got := st.Accepted + st.RejectedOversize + st.RateLimited; got != uint64(remote) {
			t.Fatalf("%d messages from peers, but the counters add up to %d: %+v", remote, got, st)
		}
	})
}
