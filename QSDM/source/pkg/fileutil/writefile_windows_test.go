//go:build windows

package fileutil

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWriteFileAtomic_CachesDirectFallbackWhenReplaceDenied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	directWriteKey := filepath.Clean(dir)
	directWriteDirectories.Delete(directWriteKey)

	originalReplace := replaceFileForWrite
	var replaceCalls int32
	replaceFileForWrite = func(string, string) error {
		atomic.AddInt32(&replaceCalls, 1)
		return windows.ERROR_ACCESS_DENIED
	}
	t.Cleanup(func() {
		replaceFileForWrite = originalReplace
		directWriteDirectories.Delete(directWriteKey)
	})

	if err := WriteFileAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&replaceCalls); got != 1 {
		t.Fatalf("replace calls = %d, want 1 before direct-write cache", got)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Fatalf("content = %q, want second", got)
	}

	payloads := [][]byte{
		bytes.Repeat([]byte("a"), 32*1024),
		bytes.Repeat([]byte("b"), 32*1024),
		bytes.Repeat([]byte("c"), 32*1024),
	}
	var wg sync.WaitGroup
	for _, payload := range payloads {
		payload := payload
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := WriteFileAtomic(path, payload, 0o600); err != nil {
				t.Errorf("concurrent direct write: %v", err)
			}
		}()
	}
	wg.Wait()
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, payload := range payloads {
		matched = matched || bytes.Equal(got, payload)
	}
	if !matched {
		t.Fatalf("direct fallback produced a torn write: got %d bytes", len(got))
	}
}
