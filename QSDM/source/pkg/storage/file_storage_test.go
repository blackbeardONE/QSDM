package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStorage_StoreTransaction_dedupeByWalletID(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]interface{}{
		"id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"x":  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.StoreTransaction(payload); err != nil {
		t.Fatal(err)
	}
	if err := fs.StoreTransaction(payload); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file after duplicate store, got %d", len(entries))
	}
	if !filepath.HasPrefix(entries[0].Name(), "wallet_tx_") {
		t.Fatalf("unexpected filename %q", entries[0].Name())
	}
}
