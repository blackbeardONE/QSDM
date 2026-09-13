package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicStrictNeverTruncatesOnReplaceFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journal.json")
	if err := WriteFileAtomicStrict(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := replaceFileForWrite
	replaceFileForWrite = func(string, string) error { return errors.New("replacement refused") }
	directWriteDirectories.Store(filepath.Clean(dir), struct{}{})
	t.Cleanup(func() {
		replaceFileForWrite = original
		directWriteDirectories.Delete(filepath.Clean(dir))
	})
	if err := WriteFileAtomicStrict(path, []byte("conflicting"), 0o600); err == nil {
		t.Fatal("strict write accepted a failed replacement")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "original" {
		t.Fatalf("strict failure changed original: %q, %v", raw, err)
	}
}
