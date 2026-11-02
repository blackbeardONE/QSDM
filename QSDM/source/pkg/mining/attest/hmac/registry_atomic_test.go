package hmac

import (
	"sync"
	"testing"
	"time"
)

var _ NonceStore = (*InMemoryNonceStore)(nil)

// atomicRaceWorkers is the HL1 WP2 acceptance fan-out: this many concurrent
// claimants sharing one nonce must produce exactly one acceptance.
const atomicRaceWorkers = 200

func TestNonceStoreTryRecordConcurrentSingleWinner(t *testing.T) {
	store := NewInMemoryNonceStore(time.Minute)
	now := time.Unix(1700000000, 0)
	nonce := [32]byte{1}
	start := make(chan struct{})
	result := make(chan bool, atomicRaceWorkers)
	var ready sync.WaitGroup
	ready.Add(atomicRaceWorkers)
	for i := 0; i < atomicRaceWorkers; i++ {
		go func() {
			ready.Done()
			<-start
			result <- store.TryRecord("node", nonce, now)
		}()
	}
	ready.Wait()
	close(start)
	winners := 0
	for i := 0; i < atomicRaceWorkers; i++ {
		if <-result {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("claims accepted=%d, want 1", winners)
	}
}

func TestNonceStoreTryRecordRetentionBoundaryWithoutDuplicateRefresh(t *testing.T) {
	store := NewInMemoryNonceStore(10 * time.Second)
	now := time.Unix(1700000000, 0)
	nonce := [32]byte{1}
	if !store.TryRecord("node", nonce, now) {
		t.Fatal("initial claim rejected")
	}
	for _, at := range []time.Time{now.Add(9 * time.Second), now.Add(10 * time.Second)} {
		if store.TryRecord("node", nonce, at) {
			t.Fatalf("retained duplicate accepted at %v", at)
		}
	}
	if !store.TryRecord("node", nonce, now.Add(10*time.Second+time.Nanosecond)) {
		t.Fatal("claim remained blocked beyond original retention; duplicate may have refreshed timestamp")
	}
	if !store.TryRecord("other-node", nonce, now.Add(10*time.Second+time.Nanosecond)) {
		t.Fatal("different node must own a separate nonce claim")
	}
	if !store.TryRecord("node", [32]byte{2}, now.Add(10*time.Second+time.Nanosecond)) {
		t.Fatal("different nonce must own a separate claim")
	}
}

func TestNonceStoreTryRecordSeenRecordCompatibility(t *testing.T) {
	store := NewInMemoryNonceStore(10 * time.Second)
	now := time.Unix(1700000000, 0)
	nonce := [32]byte{1}
	store.Record("node", nonce, now)
	if !store.Seen("node", nonce) {
		t.Fatal("Record not visible to Seen")
	}
	if store.TryRecord("node", nonce, now.Add(time.Second)) {
		t.Fatal("claim ignored prior Record")
	}
	// Legacy Record remains unconditional and refreshes its timestamp.
	store.Record("node", nonce, now.Add(9*time.Second))
	if store.TryRecord("node", nonce, now.Add(11*time.Second)) {
		t.Fatal("legacy Record timestamp not retained")
	}
	// A timestamped write evicts expired entries. Seen itself does not read a wall clock.
	store.Record("other", [32]byte{2}, now.Add(20*time.Second))
	if store.Seen("node", nonce) {
		t.Fatal("expired nonce not evicted")
	}
	if !store.TryRecord("node", nonce, now.Add(20*time.Second)) {
		t.Fatal("expired legacy claim rejected")
	}
}
