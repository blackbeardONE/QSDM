package fileutil

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteFileAtomic_ReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".state.json.*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}
}

func TestWriteFileAtomic_ConcurrentWritersNeverTearDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	payloads := [][]byte{
		bytes.Repeat([]byte("a"), 32*1024),
		bytes.Repeat([]byte("b"), 32*1024),
		bytes.Repeat([]byte("c"), 32*1024),
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(payloads))
	for _, payload := range payloads {
		payload := payload
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- WriteFileAtomic(path, payload, 0o600)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, payload := range payloads {
		matched = matched || bytes.Equal(got, payload)
	}
	if !matched {
		t.Fatalf("destination contains a torn write: got %d bytes", len(got))
	}
}
