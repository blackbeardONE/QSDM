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

	deadline := time.Now().Add(3 * time.Second)
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
