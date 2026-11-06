package chain

// Under a producer transition every signed block is verified before the
// chain is restored, and every block of an epoch carries the same producer
// key. Each key must be unpacked for ML-DSA-87 once per run, not once per
// signature: in the circl backend one unpack allocates the key's 57 KB
// matrix, and at height 880,376 of the public chain unpacking was 26% of a
// start's CPU. These tests see it through the memory a run allocates per
// signed block, with the entry points only, so that they run unchanged
// against the code before the fix (where they fail).

import (
	"runtime"
	"testing"
)

// minAllocatedBytes returns the fewest bytes f allocated in rounds runs.
// Other goroutines may allocate during a run; the minimum is the run that
// they disturbed least.
func minAllocatedBytes(rounds int, f func()) uint64 {
	var least uint64
	for i := 0; i < rounds; i++ {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		f()
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; i == 0 || n < least {
			least = n
		}
	}
	return least
}

// Ceilings per signed block. Unpacking an ML-DSA-87 public key allocates
// 57 KB. With each key unpacked once per run, the chain check allocates
// about 4 KB per signed block, and the startup load 15 to 20 KB more than
// decoding the same journal without verifying it (mostly its own copy of
// each line); with a key unpacked for every signature, about 60 and 71 KB.
const (
	chainCheckBytesPerSignedBlock  = 16 << 10
	journalLoadBytesPerSignedBlock = 40 << 10
)

// TestTransitionChainCheckUnpacksEachProducerKeyOnce: the chain check of the
// snapshot import and the tail replay (VerifyProducerTransitionChain) does
// not unpack the producer key again for every signed block.
func TestTransitionChainCheckUnpacksEachProducerKeyOnce(t *testing.T) {
	f := newJournalFixture(t, 72)
	signed := uint64(len(f.blocks) - jfSigFrom)
	loaded, err := LoadChainNDJSON(f.path)
	if err != nil {
		t.Fatal(err)
	}
	bp := f.producer(t, nil)
	perBlock := minAllocatedBytes(3, func() {
		if _, err := bp.VerifyProducerTransitionChain(loaded); err != nil {
			t.Fatal(err)
		}
	}) / signed
	t.Logf("VerifyProducerTransitionChain: %d bytes per signed block (%d signed blocks)", perBlock, signed)
	if perBlock > chainCheckBytesPerSignedBlock {
		t.Fatalf("VerifyProducerTransitionChain allocates %d bytes per signed block, more than %d: is the producer key unpacked for every signature?",
			perBlock, chainCheckBytesPerSignedBlock)
	}
}

// TestJournalLoadUnpacksEachProducerKeyOnce: the startup load
// (LoadVerifiedJournal, on one worker and on four) does not unpack the
// producer key again for every signed block. LoadChainNDJSON, which decodes
// the same journal without verifying it, is the baseline.
func TestJournalLoadUnpacksEachProducerKeyOnce(t *testing.T) {
	f := newJournalFixture(t, 72)
	signed := uint64(len(f.blocks) - jfSigFrom)
	decode := minAllocatedBytes(3, func() {
		if _, err := LoadChainNDJSON(f.path); err != nil {
			t.Fatal(err)
		}
	})
	bp := f.producer(t, nil)
	for _, workers := range []int{1, 4} {
		load := minAllocatedBytes(3, func() {
			if _, err := bp.loadVerifiedJournal(f.path, workers); err != nil {
				t.Fatal(err)
			}
		})
		if load < decode {
			t.Fatalf("%d workers: the load allocated %d bytes, less than decoding alone (%d)", workers, load, decode)
		}
		perBlock := (load - decode) / signed
		t.Logf("%d workers: LoadVerifiedJournal allocates %d bytes per signed block more than LoadChainNDJSON (%d signed blocks)", workers, perBlock, signed)
		if perBlock > journalLoadBytesPerSignedBlock {
			t.Fatalf("%d workers: LoadVerifiedJournal allocates %d bytes per signed block more than decoding the journal, more than %d: is the producer key unpacked for every signature?",
				workers, perBlock, journalLoadBytesPerSignedBlock)
		}
	}
}
