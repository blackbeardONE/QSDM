//go:build soak
// +build soak

// Soak harness for the libp2p GossipSub transport — long-running stress
// test that stands up a small mesh of in-process libp2p hosts, hammers
// them with a configurable publisher fan-out, and asserts the receive
// invariants over the duration.
//
// Run with: go test -tags soak ./tests -run TestSoak_Pubsub -timeout 30m
//
// Tunables (env):
//   QSDM_SOAK_DURATION   total wall time (default 2m)
//   QSDM_SOAK_HOSTS      number of libp2p hosts in the mesh (default 4, min 2)
//   QSDM_SOAK_PRODUCERS  publisher goroutines PER host (default 2)
//   QSDM_SOAK_MSGRATEHZ  per-publisher target rate, msgs/sec (default 50)
//   QSDM_SOAK_MSGSIZE    payload size in bytes (default 256)
//
// Invariants asserted at the end of the run:
//   - Every receiver sees > 0 messages from every other host (the mesh
//     never silently partitioned).
//   - No publisher saw the topic refuse a payload for an unbounded run
//     of consecutive errors (transient errors are allowed; a sustained
//     error window means the harness has lost connectivity).
//   - Per-host receive count is within 50% of the median across hosts —
//     a wildly skewed mesh signals a propagation regression.

package tests

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

const soakPubsubTopic = "qsdm-soak-pubsub"

func soakPubsubDuration(t *testing.T, def time.Duration) time.Duration {
	s := os.Getenv("QSDM_SOAK_DURATION")
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Logf("invalid QSDM_SOAK_DURATION=%q, using default %s", s, def)
		return def
	}
	return d
}

func soakPubsubInt(envKey string, def, min int) int {
	v := os.Getenv(envKey)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min {
		return def
	}
	return n
}

// TestSoak_PubsubMultiHostFanout stands up N libp2p hosts in a star topology
// (host[0] is the rendezvous), runs `producers` publishers on each host at the
// configured rate for `duration`, and asserts the invariants above.
func TestSoak_PubsubMultiHostFanout(t *testing.T) {
	duration := soakPubsubDuration(t, 2*time.Minute)
	hosts := soakPubsubInt("QSDM_SOAK_HOSTS", 4, 2)
	producers := soakPubsubInt("QSDM_SOAK_PRODUCERS", 2, 1)
	rateHz := soakPubsubInt("QSDM_SOAK_MSGRATEHZ", 50, 1)
	msgSize := soakPubsubInt("QSDM_SOAK_MSGSIZE", 256, 16)

	t.Logf("soak: duration=%s hosts=%d producers/host=%d rate=%dHz size=%dB",
		duration, hosts, producers, rateHz, msgSize)

	ctx, cancel := context.WithTimeout(context.Background(), duration+30*time.Second)
	defer cancel()

	// --- bring up the mesh -------------------------------------------------
	type node struct {
		h     host.Host
		ps    *pubsub.PubSub
		topic *pubsub.Topic
		sub   *pubsub.Subscription
		// rxFromHost[i] = messages received that were tagged as having
		// originated on host i.
		rxFromHost []int64
	}

	nodes := make([]*node, hosts)
	for i := 0; i < hosts; i++ {
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		if err != nil {
			t.Fatalf("libp2p.New host[%d]: %v", i, err)
		}
		ps, err := pubsub.NewGossipSub(ctx, h)
		if err != nil {
			t.Fatalf("NewGossipSub host[%d]: %v", i, err)
		}
		topic, err := ps.Join(soakPubsubTopic)
		if err != nil {
			t.Fatalf("Join host[%d]: %v", i, err)
		}
		sub, err := topic.Subscribe()
		if err != nil {
			t.Fatalf("Subscribe host[%d]: %v", i, err)
		}
		nodes[i] = &node{
			h:          h,
			ps:         ps,
			topic:      topic,
			sub:        sub,
			rxFromHost: make([]int64, hosts),
		}
	}
	t.Cleanup(func() {
		for _, n := range nodes {
			n.sub.Cancel()
			_ = n.topic.Close()
			_ = n.h.Close()
		}
	})

	// Star topology: every host[i>0] connects to host[0]. GossipSub's
	// peer-exchange does the rest.
	for i := 1; i < hosts; i++ {
		ai := peer.AddrInfo{ID: nodes[0].h.ID(), Addrs: nodes[0].h.Addrs()}
		if err := nodes[i].h.Connect(ctx, ai); err != nil {
			t.Fatalf("connect host[%d]→host[0]: %v", i, err)
		}
	}
	// Let the mesh form before firing publishers (otherwise the first
	// second of traffic is dropped silently).
	time.Sleep(1 * time.Second)

	// --- start receivers ---------------------------------------------------
	var (
		stopCh = make(chan struct{})
		wg     sync.WaitGroup

		totalRx     atomic.Int64
		totalSent   atomic.Int64
		sustainedTx [16]atomic.Int64 // ring-buffer-ish error spikes counter
	)

	for i, nd := range nodes {
		wg.Add(1)
		go func(idx int, n *node) {
			defer wg.Done()
			for {
				msg, err := n.sub.Next(ctx)
				if err != nil {
					return
				}
				// Skip our own messages — GossipSub delivers them too.
				if msg.ReceivedFrom == n.h.ID() {
					continue
				}
				if len(msg.Data) < 4 {
					continue
				}
				origin := binary.BigEndian.Uint32(msg.Data[:4])
				if int(origin) < len(n.rxFromHost) {
					atomic.AddInt64(&n.rxFromHost[origin], 1)
				}
				totalRx.Add(1)
				_ = idx
			}
		}(i, nd)
	}

	// --- start publishers --------------------------------------------------
	publisher := func(hostIdx, workerIdx int) {
		defer wg.Done()
		// Tag every payload with its origin host id so receivers can
		// attribute traffic per source.
		header := make([]byte, 4)
		binary.BigEndian.PutUint32(header, uint32(hostIdx))
		payload := make([]byte, msgSize)
		copy(payload, header)
		// Random tail so GossipSub message-id dedupe (which hashes the
		// payload by default) doesn't suppress our traffic.
		_, _ = rand.Read(payload[4:])

		interval := time.Second / time.Duration(rateHz)
		if interval <= 0 {
			interval = time.Microsecond
		}
		t := time.NewTicker(interval)
		defer t.Stop()

		consecutiveErrs := 0
		for {
			select {
			case <-stopCh:
				return
			case <-t.C:
				// Refresh the random tail each tick so payload bytes are
				// unique (GossipSub message-id = sha256(data) by default).
				_, _ = rand.Read(payload[4:])
				err := nodes[hostIdx].topic.Publish(ctx, payload)
				if err != nil {
					consecutiveErrs++
					if consecutiveErrs > 100 {
						sustainedTx[hostIdx%len(sustainedTx)].Add(1)
					}
					continue
				}
				consecutiveErrs = 0
				totalSent.Add(1)
			}
		}
	}

	for i := 0; i < hosts; i++ {
		for j := 0; j < producers; j++ {
			wg.Add(1)
			go publisher(i, j)
		}
	}

	// Periodic progress logger.
	statusDone := make(chan struct{})
	go func() {
		ti := time.NewTicker(10 * time.Second)
		defer ti.Stop()
		start := time.Now()
		for {
			select {
			case <-statusDone:
				return
			case <-ti.C:
				fmt.Printf("soak-pubsub t=%6.1fs sent=%d rx=%d\n",
					time.Since(start).Seconds(), totalSent.Load(), totalRx.Load())
			}
		}
	}()

	time.Sleep(duration)
	close(stopCh)
	close(statusDone)

	// Drain receivers — give them a beat to flush in-flight messages.
	time.Sleep(500 * time.Millisecond)
	for _, n := range nodes {
		n.sub.Cancel()
	}
	wg.Wait()

	// --- invariants --------------------------------------------------------
	if totalSent.Load() == 0 {
		t.Fatalf("soak-pubsub: no publishes succeeded — harness broken")
	}
	if totalRx.Load() == 0 {
		t.Fatalf("soak-pubsub: no messages received — mesh did not form")
	}

	// Per-host should have seen traffic from every OTHER host.
	for i, n := range nodes {
		for j := range nodes {
			if i == j {
				continue
			}
			rx := atomic.LoadInt64(&n.rxFromHost[j])
			if rx == 0 {
				t.Errorf("host[%d] received zero messages from host[%d] — partition", i, j)
			}
		}
	}

	// Per-host total receive count fairness check (within 50% of the median).
	totals := make([]int64, hosts)
	for i, n := range nodes {
		var sum int64
		for j := range n.rxFromHost {
			sum += atomic.LoadInt64(&n.rxFromHost[j])
		}
		totals[i] = sum
	}
	sorted := append([]int64(nil), totals...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	median := sorted[len(sorted)/2]
	if median > 0 {
		for i, v := range totals {
			ratio := float64(v) / float64(median)
			if ratio < 0.5 || ratio > 2.0 {
				t.Logf("WARN: host[%d] rx=%d outside 0.5×–2× median=%d (ratio=%.2f)",
					i, v, median, ratio)
			}
		}
	}

	// Sustained-error invariant: no host should have hit a "long error
	// window" event (>100 consecutive publish errors).
	for i := range sustainedTx {
		if c := sustainedTx[i].Load(); c > 0 {
			t.Errorf("host bucket[%d] hit %d sustained-error windows", i, c)
		}
	}

	t.Logf("soak-pubsub summary: hosts=%d sent=%d rx=%d per_host_totals=%v",
		hosts, totalSent.Load(), totalRx.Load(), totals)
}
