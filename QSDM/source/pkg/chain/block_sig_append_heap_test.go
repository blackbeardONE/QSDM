package chain

// A synthetic measurement of the heap the blocks a node appends while it runs
// keep, with their signatures in the journal (KeepSignaturesInJournal) and in
// memory (no journal attached: the behaviour before it), for the producer
// (ProduceBlock) and for a full node (TryAppendExternalBlock of the blocks
// decoded from their JSON). Every block is signed with ML-DSA-87 under the
// fixture's producer transition (verified_chain_test.go).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// appendHeapBlocks is how many blocks each run of TestAppendedSignaturesHeap
// appends; QSDM_APPEND_HEAP_BLOCKS sets another number.
const appendHeapBlocks = 2000

// heapRun is one run's measurement: what the heap grew by over the appends,
// after a full collection.
type heapRun struct {
	name        string
	blocks      int
	alloc       int64 // HeapAlloc growth: the bytes of the objects the appended blocks keep
	inuse       int64 // HeapInuse growth: the bytes of the spans that hold them
	sigBytes    int   // producer signature bytes the chain holds in memory at the end
	stripped    int   // blocks of the chain whose signature is only in the journal
	took        time.Duration
	journalSize int64
}

func (h heapRun) String() string {
	return fmt.Sprintf("%-36s %5d blocks in %6.2fs: heap alloc +%7.2f MiB (%5d B/block), heap in use +%7.2f MiB (%5d B/block); "+
		"signatures in memory %8d B; stripped %d; journal %.1f MiB",
		h.name, h.blocks, h.took.Seconds(), mib(h.alloc), h.alloc/int64(h.blocks), mib(h.inuse), h.inuse/int64(h.blocks),
		h.sigBytes, h.stripped, mib(h.journalSize))
}

func mib(n int64) float64 { return float64(n) / (1 << 20) }

// heapNow collects the garbage, twice so that nothing a finalizer kept is
// left, and returns HeapAlloc and HeapInuse.
func heapNow() (alloc, inuse int64) {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc), int64(ms.HeapInuse)
}

// measureAppends restores the whole fixture on a producer whose hook journals
// every block, attaching the journal if attach, then measures the heap over
// appendOne called n times. The journal's fsync is skipped: it allocates
// nothing, and at about 5 ms per block on a laptop it would make the runs
// slow.
func measureAppends(t *testing.T, f journalFixture, name string, n int, attach bool, appendOne func(r *appendRig, i int)) (heapRun, [][]byte) {
	t.Helper()
	r := newAppendRig(t, f, len(f.blocks), attach)
	r.j.sync = func(*os.File) error { return nil }
	r.sealed = nil // the test keeps no appended block: only the chain holds them
	run := heapRun{name: name, blocks: n}
	alloc0, inuse0 := heapNow()
	start := time.Now()
	for i := 0; i < n; i++ {
		appendOne(r, i)
	}
	run.took = time.Since(start)
	alloc1, inuse1 := heapNow()
	// What appendOne reads (the received blocks' lines) was live at alloc0:
	// it must not be collected before alloc1 either.
	runtime.KeepAlive(appendOne)
	run.alloc, run.inuse = alloc1-alloc0, inuse1-inuse0
	if r.failed != 0 {
		t.Fatalf("%s: %d journal appends failed", name, r.failed)
	}
	for _, b := range r.bp.AllBlocks() {
		run.sigBytes += len(b.ProducerAuth.Signature)
		if b.ProducerAuth.Stripped() {
			run.stripped++
		}
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	run.journalSize = int64(len(data))
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))[len(f.blocks):]
	// Let the producer go: the journal (closed again by its cleanup) would
	// keep it, and its chain, alive until the test ends.
	r.j.Close()
	r.j.mu.Lock()
	r.j.keep = nil
	r.j.mu.Unlock()
	return run, lines
}

// TestAppendedSignaturesHeap appends a few thousand signed blocks under a
// producer transition (200 with -short) and reports the heap they keep with
// and without the journal attached: with it, an appended block keeps about
// the heap of a block without its 4,627-byte signature (4,864 bytes
// allocated); without it, the signature stays in memory until the next start.
//
//	go test -tags dilithium_circl -run TestAppendedSignaturesHeap -v ./pkg/chain/
func TestAppendedSignaturesHeap(t *testing.T) {
	n := appendHeapBlocks
	if testing.Short() {
		n = 200
	}
	if s := os.Getenv("QSDM_APPEND_HEAP_BLOCKS"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 10 {
			t.Fatalf("QSDM_APPEND_HEAP_BLOCKS=%q: want a number of blocks, at least 10", s)
		}
		n = v
	}
	f := newJournalFixture(t, jfCheckpoint+2)
	produce := func(r *appendRig, i int) {
		h := r.bp.TipHeight() + 1
		tx := &mempool.Tx{ID: fmt.Sprintf("heap-%d", h), Sender: "alice", Recipient: shareTestRecipients[h%3], Amount: 1, Nonce: h,
			ContractID: "qsdm/journal-test"}
		if err := r.bp.pool.Add(tx); err != nil {
			t.Fatal(err)
		}
		if _, err := r.bp.ProduceBlock(); err != nil {
			t.Fatalf("ProduceBlock at %d: %v", h, err)
		}
	}
	producedKept, lines := measureAppends(t, f, "produced, signatures in memory", n, false, produce)
	producedJournal, _ := measureAppends(t, f, "produced, signatures in the journal", n, true, produce)
	receive := func(r *appendRig, i int) {
		blk := &Block{}
		if err := json.Unmarshal(lines[i], blk); err != nil {
			t.Fatal(err)
		}
		if err := r.bp.TryAppendExternalBlock(blk); err != nil {
			t.Fatalf("append %d: %v", blk.Height, err)
		}
	}
	receivedKept, _ := measureAppends(t, f, "received, signatures in memory", n, false, receive)
	receivedJournal, _ := measureAppends(t, f, "received, signatures in the journal", n, true, receive)

	for _, run := range []heapRun{producedKept, producedJournal, receivedKept, receivedJournal} {
		t.Log(run)
	}
	for _, pair := range [][2]heapRun{{producedKept, producedJournal}, {receivedKept, receivedJournal}} {
		kept, journal := pair[0], pair[1]
		// Every appended block but the tip lost its signature (the tip the
		// start restored keeps its own), and the heap kept at least 4,000
		// bytes per block less: the signature is 4,627 bytes in a 4,864-byte
		// allocation, the stripped copy adds 32.
		if journal.sigBytes != 2*4627 || journal.stripped < n-1 {
			t.Fatalf("%s: %d signature bytes in memory, %d blocks stripped", journal.name, journal.sigBytes, journal.stripped)
		}
		if kept.stripped > len(f.blocks) || kept.sigBytes < n*4627 {
			t.Fatalf("%s: %d signature bytes in memory, %d blocks stripped", kept.name, kept.sigBytes, kept.stripped)
		}
		if saved := (kept.alloc - journal.alloc) / int64(n); saved < 4000 {
			t.Fatalf("%s keeps only %d bytes per block less than %s", journal.name, saved, kept.name)
		}
	}
}
