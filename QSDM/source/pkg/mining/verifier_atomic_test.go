package mining

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// atomicRaceWorkers is the HL1 WP2 acceptance fan-out: this many concurrent
// submissions of the same proof must produce exactly one acceptance.
const atomicRaceWorkers = 200

// Every verifier call reaches the last validation step before any may claim
// the ID. This makes the Seen/Record race deterministic rather than relying on
// the scheduler to overlap proof computations.
type atomicProofBarrierBatches struct {
	entered chan<- struct{}
	release <-chan struct{}
}

func (b atomicProofBarrierBatches) ValidateBatch(_ Batch) error {
	b.entered <- struct{}{}
	<-b.release
	return nil
}

func TestVerifyConcurrentProofHasOneAcceptance(t *testing.T) {
	v, raw, _ := buildMiniSetup(t)
	proof, err := ParseProof(raw)
	if err != nil {
		t.Fatal(err)
	}
	wantID, err := proof.ID()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, atomicRaceWorkers)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	v.cfg.Batches = atomicProofBarrierBatches{entered: entered, release: release}

	type result struct {
		id  [32]byte
		err error
	}
	results := make(chan result, atomicRaceWorkers)
	for i := 0; i < atomicRaceWorkers; i++ {
		go func() {
			id, err := v.Verify(raw, proof.Height)
			results <- result{id: id, err: err}
		}()
	}
	timeout := time.NewTimer(60 * time.Second)
	defer timeout.Stop()
	for i := 0; i < atomicRaceWorkers; i++ {
		select {
		case <-entered:
		case r := <-results:
			t.Fatalf("verifier call returned before the final validation barrier: %v", r.err)
		case <-timeout.C:
			t.Fatal("verifier calls did not all reach the final validation barrier")
		}
	}
	if v.cfg.Dedup.Seen(wantID) {
		t.Fatal("proof ID was claimed before validation completed")
	}
	unblock()

	accepted, duplicates := 0, 0
	for i := 0; i < atomicRaceWorkers; i++ {
		select {
		case r := <-results:
			if r.id != wantID {
				t.Fatalf("returned ID = %x, want %x", r.id, wantID)
			}
			if r.err == nil {
				accepted++
				continue
			}
			var rejection *RejectError
			if !errors.As(r.err, &rejection) || rejection.Reason != ReasonDuplicate {
				t.Fatalf("losing request must report duplicate, got %v", r.err)
			}
			duplicates++
		case <-timeout.C:
			t.Fatal("concurrent proof verification did not complete")
		}
	}
	if accepted != 1 || duplicates != atomicRaceWorkers-1 {
		t.Fatalf("accepted=%d duplicates=%d, want 1 and %d", accepted, duplicates, atomicRaceWorkers-1)
	}
	if !v.cfg.Dedup.Seen(wantID) || v.cfg.Dedup.Size() != 1 {
		t.Fatal("accepted proof must leave exactly one dedup entry")
	}
	id, err := v.Verify(raw, proof.Height)
	var rejection *RejectError
	if id != wantID || !errors.As(err, &rejection) || rejection.Reason != ReasonDuplicate {
		t.Fatalf("subsequent replay ID=%x error=%v, want original ID and duplicate", id, err)
	}
}

func TestVerifyInvalidProofDoesNotClaimID(t *testing.T) {
	v, raw, _ := buildMiniSetup(t)
	proof, err := ParseProof(raw)
	if err != nil {
		t.Fatal(err)
	}
	wantID, err := proof.ID()
	if err != nil {
		t.Fatal(err)
	}
	// This rejection happens after the dedup lookup and all PoW checks.
	v.cfg.Batches = fraudBatches{}
	id, err := v.Verify(raw, proof.Height)
	var rejection *RejectError
	if id != wantID || !errors.As(err, &rejection) || rejection.Reason != ReasonBatchFraud {
		t.Fatalf("invalid batch ID=%x error=%v, want original ID and batch-fraud", id, err)
	}
	if v.cfg.Dedup.Seen(wantID) || v.cfg.Dedup.Size() != 0 {
		t.Fatal("rejected proof claimed a dedup entry")
	}
	// Reuse the dedup set with corrected injected dependencies to prove the
	// rejection did not consume this proof ID. The fraud quarantine is
	// separately enforced and must also be reset for this isolated check.
	v.cfg.Batches = goodBatches{}
	v.cfg.Quarantine = NewQuarantineSet()
	if id, err := v.Verify(raw, proof.Height); err != nil || id != wantID {
		t.Fatalf("proof after corrected validation ID=%x error=%v", id, err)
	}
}

func TestProofIDSetTryRecordConcurrentSingleWinner(t *testing.T) {
	s := NewProofIDSet(1024)
	id := [32]byte{0xAB}
	start := make(chan struct{})
	result := make(chan bool, atomicRaceWorkers)
	var ready sync.WaitGroup
	ready.Add(atomicRaceWorkers)
	for i := 0; i < atomicRaceWorkers; i++ {
		go func() {
			ready.Done()
			<-start
			result <- s.TryRecord(id, 42)
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
	if winners != 1 || s.Size() != 1 {
		t.Fatalf("claims accepted=%d size=%d, want 1 and 1", winners, s.Size())
	}
}

func TestProofIDSetTryRecordRetentionAndRecordCompatibility(t *testing.T) {
	s := NewProofIDSet(10)
	first, second, boundary, next := [32]byte{1}, [32]byte{2}, [32]byte{3}, [32]byte{4}
	if !s.TryRecord(first, 1) || s.TryRecord(first, 9) {
		t.Fatal("first claim must win and a repeated claim must lose")
	}
	if !s.TryRecord(second, 10) || !s.TryRecord(boundary, 11) || !s.Seen(first) {
		t.Fatal("ID exactly at the retention cutoff must remain recorded")
	}
	if !s.TryRecord(next, 12) || s.Seen(first) {
		t.Fatal("expired ID must be evicted; a rejected claim must not refresh its height")
	}
	if !s.Seen(second) || !s.Seen(boundary) || !s.Seen(next) {
		t.Fatal("retention evicted current IDs")
	}
	if !s.TryRecord(first, 12) {
		t.Fatal("ID may be claimed again after its retention window has expired")
	}
	// Existing Record callers retain overwrite/refresh semantics and share the
	// same dedup namespace with the atomic claimant.
	legacy := [32]byte{5}
	s.Record(legacy, 13)
	if !s.Seen(legacy) || s.TryRecord(legacy, 14) {
		t.Fatal("Record entry must be visible to Seen and block TryRecord")
	}
	s.Record(legacy, 30)
	if s.Size() != 1 || !s.Seen(legacy) {
		t.Fatal("Record must still refresh existing IDs and evict stale entries")
	}
	if !s.TryRecord([32]byte{6}, 40) || !s.Seen(legacy) {
		t.Fatal("refreshed Record entry must remain at its retention boundary")
	}
	if !s.TryRecord([32]byte{7}, 41) || s.Seen(legacy) {
		t.Fatal("refreshed Record entry must expire after its retention boundary")
	}
}
