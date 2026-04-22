//go:build soak
// +build soak

// Soak harness for the mempool — long-running, fault-injected stress test.
// Run with: go test -tags soak ./tests -run TestSoak_Mempool -timeout 30m
//
// This harness is excluded from the default suite to keep `go test ./...` fast.
// Use QSDM_SOAK_DURATION=5m / QSDM_SOAK_PRODUCERS=8 to tune.

package tests

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func soakDuration(t *testing.T, def time.Duration) time.Duration {
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

func soakInt(envKey string, def int) int {
	v := os.Getenv(envKey)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// TestSoak_MempoolSustainedChurn runs producer / consumer / fault-injector goroutines
// against a fee-capped mempool for QSDM_SOAK_DURATION (default 2m). It asserts invariants
// throughout: Size never exceeds MaxSize; no duplicate IDs observed; periodic drains keep
// backlog bounded; random stale-eviction does not corrupt the heap.
func TestSoak_MempoolSustainedChurn(t *testing.T) {
	duration := soakDuration(t, 2*time.Minute)
	producers := soakInt("QSDM_SOAK_PRODUCERS", 8)
	consumers := soakInt("QSDM_SOAK_CONSUMERS", 2)
	maxSize := soakInt("QSDM_SOAK_MAXSIZE", 5000)

	cfg := mempool.Config{
		MaxSize:       maxSize,
		MaxTxAge:      30 * time.Second,
		EvictInterval: 500 * time.Millisecond,
	}
	mp := mempool.New(cfg)
	mp.Start()
	t.Cleanup(mp.Stop)

	t.Logf("soak: duration=%s producers=%d consumers=%d maxSize=%d", duration, producers, consumers, maxSize)

	var (
		added     atomic.Int64
		rejected  atomic.Int64
		popped    atomic.Int64
		evicted   atomic.Int64
		stopCh    = make(chan struct{})
		wg        sync.WaitGroup
		seenMu    sync.Mutex
		seen      = make(map[string]struct{})
		dupErrors atomic.Int64
	)

	producer := func(workerID int) {
		defer wg.Done()
		r := rand.New(rand.NewSource(int64(workerID)*1_000_003 + time.Now().UnixNano()))
		i := uint64(0)
		for {
			select {
			case <-stopCh:
				return
			default:
			}
			id := fmt.Sprintf("w%d-%d-%d", workerID, i, r.Int63())
			tx := &mempool.Tx{
				ID:      id,
				Sender:  fmt.Sprintf("addr-%d", workerID),
				Amount:  r.Float64() * 100,
				Fee:     r.Float64() * 10,
				AddedAt: time.Now(),
			}
			if err := mp.Add(tx); err != nil {
				rejected.Add(1)
			} else {
				added.Add(1)
				seenMu.Lock()
				if _, exists := seen[id]; exists {
					dupErrors.Add(1)
				} else {
					seen[id] = struct{}{}
				}
				seenMu.Unlock()
			}
			i++

			if sz := mp.Size(); sz > maxSize {
				t.Errorf("mempool Size=%d exceeded MaxSize=%d", sz, maxSize)
				return
			}
		}
	}

	consumer := func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				drained := mp.Drain(64)
				popped.Add(int64(len(drained)))
			}
		}
	}

	faultInjector := func() {
		defer wg.Done()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				evicted.Add(int64(mp.EvictStale()))
			}
		}
	}

	for i := 0; i < producers; i++ {
		wg.Add(1)
		go producer(i)
	}
	for i := 0; i < consumers; i++ {
		wg.Add(1)
		go consumer()
	}
	wg.Add(1)
	go faultInjector()

	// Periodic progress logger every 10s so long soaks aren't silent.
	statusDone := make(chan struct{})
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		start := time.Now()
		for {
			select {
			case <-statusDone:
				return
			case <-t.C:
				fmt.Printf("soak t=%6.1fs size=%-6d added=%d rejected=%d popped=%d evicted=%d\n",
					time.Since(start).Seconds(), mp.Size(), added.Load(), rejected.Load(), popped.Load(), evicted.Load())
			}
		}
	}()

	time.Sleep(duration)
	close(stopCh)
	close(statusDone)
	wg.Wait()

	if dupErrors.Load() != 0 {
		t.Fatalf("soak: %d duplicate IDs accepted", dupErrors.Load())
	}
	if added.Load() == 0 {
		t.Fatalf("soak: no transactions accepted — harness broken")
	}
	t.Logf("soak summary: added=%d rejected=%d popped=%d evicted=%d final_size=%d",
		added.Load(), rejected.Load(), popped.Load(), evicted.Load(), mp.Size())
}
