package contracts

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCallTracer_StartTraceCompactionLoop_CompactsWhenLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tr.ndjson")
	if err := os.WriteFile(path, make([]byte, 500), 0644); err != nil {
		t.Fatal(err)
	}

	ct := NewCallTracer(100)
	ct.ConfigureRetention(path, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ct.StartTraceCompactionLoop(ctx, 20*time.Millisecond, 400)

	// Generous wall deadline — under `go test ./...` parallel load the
	// compaction goroutine can be starved by the scheduler for hundreds of
	// milliseconds at a time. The test itself finishes in <100ms in isolation;
	// this 15s ceiling exists purely to avoid flakes under load, not to mask
	// real regressions (a broken compaction loop would never finish).
	deadline := time.Now().Add(15 * time.Second)
	var sz int64
	for time.Now().Before(deadline) {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		sz = fi.Size()
		if sz <= 400 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected compaction below threshold, got size %d", sz)
}
