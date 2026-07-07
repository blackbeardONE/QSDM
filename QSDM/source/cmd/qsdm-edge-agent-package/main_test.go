package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceArchiveWithRetryReplacesExistingFile(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.tmp")
	destination := filepath.Join(directory, "release.tar.gz")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceArchiveWithRetry(source, destination); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new" {
		t.Fatalf("destination = %q", raw)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists after replacement: %v", err)
	}
}
