package tests

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// TestIntegration_MempoolBulkFeeOrdering drains a large pool and asserts fee-priority (non-increasing on pop).
func TestIntegration_MempoolBulkFeeOrdering(t *testing.T) {
	n := 800
	if testing.Short() {
		n = 120
	}
	m := mempool.New(mempool.DefaultConfig())
	for i := 0; i < n; i++ {
		fee := float64((i*13 + 7) % 97)
		if err := m.Add(&mempool.Tx{
			ID: fmt.Sprintf("bulk-%d", i), Sender: "alice", Recipient: "bob",
			Amount: 1, Fee: fee,
		}); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	if m.Size() != n {
		t.Fatalf("size %d want %d", m.Size(), n)
	}
	tx, ok := m.Pop()
	if !ok {
		t.Fatal("expected first pop")
	}
	last := tx.Fee
	for m.Size() > 0 {
		tx, ok = m.Pop()
		if !ok {
			t.Fatal("unexpected empty")
		}
		if tx.Fee > last+1e-9 {
			t.Fatalf("heap order broken: fee %v after %v", tx.Fee, last)
		}
		last = tx.Fee
	}
}

// TestIntegration_MempoolConcurrentAdds exercises the mempool mutex under parallel inserts (partition-free stress).
func TestIntegration_MempoolConcurrentAdds(t *testing.T) {
	nPer := 64
	workers := 8
	if testing.Short() {
		nPer = 24
		workers = 4
	}
	m := mempool.New(mempool.DefaultConfig())
	var wg sync.WaitGroup
	var addErrors atomic.Int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for i := 0; i < nPer; i++ {
				id := fmt.Sprintf("c-%d-%d", wid, i)
				fee := float64((wid*17 + i*3) % 53)
				if err := m.Add(&mempool.Tx{
					ID: id, Sender: "a", Recipient: "b", Amount: 1, Fee: fee,
				}); err != nil {
					addErrors.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()
	if addErrors.Load() != 0 {
		t.Fatalf("concurrent Add failures: %d", addErrors.Load())
	}
	want := workers * nPer
	if sz := m.Size(); sz != want {
		t.Fatalf("size %d want %d", sz, want)
	}
	popped := 0
	var last = 1e12
	for m.Size() > 0 {
		tx, ok := m.Pop()
		if !ok {
			break
		}
		if tx.Fee > last+1e-9 {
			t.Fatalf("order after concurrent adds: %v after %v", tx.Fee, last)
		}
		last = tx.Fee
		popped++
	}
	if popped != want {
		t.Fatalf("popped %d want %d", popped, want)
	}
}

// TestIntegration_MempoolHighFeeEvictWhenFull fills the pool to MaxSize then adds a much higher-fee tx;
// the implementation may evict a low-fee candidate from the tail scan (see mempool.Add). Asserts size
// stays at MaxSize and the premium tx is retained.
func TestIntegration_MempoolHighFeeEvictWhenFull(t *testing.T) {
	maxSz := 200
	if testing.Short() {
		maxSz = 24
	}
	cfg := mempool.DefaultConfig()
	cfg.MaxSize = maxSz
	m := mempool.New(cfg)
	for i := 0; i < maxSz; i++ {
		if err := m.Add(&mempool.Tx{
			ID: fmt.Sprintf("tx-%d", i), Sender: "alice", Recipient: "bob",
			Amount: 1, Fee: float64(i + 1),
		}); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	if m.Size() != maxSz {
		t.Fatalf("filled size %d want %d", m.Size(), maxSz)
	}
	premiumFee := float64(maxSz * 1000)
	if err := m.Add(&mempool.Tx{
		ID: "premium", Sender: "alice", Recipient: "bob",
		Amount: 1, Fee: premiumFee,
	}); err != nil {
		t.Fatalf("premium add: %v", err)
	}
	if m.Size() != maxSz {
		t.Fatalf("after premium size %d want %d", m.Size(), maxSz)
	}
	tx, ok := m.Get("premium")
	if !ok || tx.Fee != premiumFee {
		t.Fatalf("premium missing or wrong fee: ok=%v tx=%v", ok, tx)
	}
}
