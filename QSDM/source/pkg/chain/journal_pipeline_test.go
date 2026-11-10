package chain

// Tests for journalPipeline (block_sig_journal.go), the reader and workers
// that LoadVerifiedJournal and CheckJournal run at every start.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writePipelineJournal(t *testing.T, lines int) *os.File {
	t.Helper()
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "{\"n\":%d}\n", i)
	}
	path := filepath.Join(t.TempDir(), "journal.ndjson")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// The reader holds at most journalReadAhead lines per worker ahead of the
// line the caller waits for, and every worker gets a line while the caller
// waits: a start never reads the whole journal into memory ahead of the
// checks, and no worker idles behind one slow line.
func TestJournalPipelineReadAheadIsBounded(t *testing.T) {
	for _, workers := range []int{1, 3, 6} {
		f := writePipelineJournal(t, 1000)
		release := make(chan struct{})
		var working atomic.Int32
		p := startJournalPipeline(f, workers, func(it *journalItem) {
			working.Add(1)
			<-release
		})
		want := workers * journalReadAhead
		deadline := time.Now().Add(10 * time.Second)
		for (len(p.order) < want || int(working.Load()) < workers) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond) // nothing more may be read
		if got := len(p.order); got != want {
			t.Errorf("%d workers: %d lines queued ahead of the caller, want %d", workers, got, want)
		}
		if got := int(working.Load()); got != workers {
			t.Errorf("%d workers: %d lines being worked on, want %d", workers, got, workers)
		}
		close(release)
		p.stop()
	}
}

// Every line reaches the caller once, in journal order, whatever order the
// workers finish them in.
func TestJournalPipelineKeepsJournalOrder(t *testing.T) {
	const lines = 2000
	f := writePipelineJournal(t, lines)
	p := startJournalPipeline(f, 5, func(it *journalItem) {
		if it.index%7 == 0 {
			time.Sleep(200 * time.Microsecond) // some lines take longer
		}
		it.blk = &Block{Height: it.index}
	})
	var next uint64
	for it := range p.order {
		<-it.done
		if it.index != next || it.blk == nil || it.blk.Height != next || it.lineno != int(next)+1 {
			p.stop()
			t.Fatalf("line %d came as index %d (line %d)", next, it.index, it.lineno)
		}
		next++
	}
	if err := p.wait(); err != nil {
		t.Fatal(err)
	}
	if next != lines {
		t.Fatalf("%d lines reached the caller, want %d", next, lines)
	}
}
