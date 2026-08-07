package chain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateBFTSignerPersistsIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator-consensus-key.json")
	first, created, err := LoadOrCreateBFTSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first load should create the key")
	}
	if first.Address() == "" {
		t.Fatal("created signer has no address")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("consensus signer permissions = %o, want 600", got)
		}
	}

	second, created, err := LoadOrCreateBFTSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("second load must reuse the existing key")
	}
	if second.Address() != first.Address() {
		t.Fatalf("validator identity changed: %s != %s", second.Address(), first.Address())
	}

	message := []byte("qsdm-consensus-signer-test")
	signature, err := second.Sign(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyAuth(BFTWireAuth{PublicKey: second.GetPublicKey(), Signature: signature}, message, second.Address()); err != nil {
		t.Fatalf("persisted signer did not verify: %v", err)
	}
}

func TestLoadOrCreateBFTSignerRejectsMismatchedPair(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.json")
	rightPath := filepath.Join(dir, "right.json")
	if _, _, err := LoadOrCreateBFTSigner(leftPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateBFTSigner(rightPath); err != nil {
		t.Fatal(err)
	}

	leftRaw, err := os.ReadFile(leftPath)
	if err != nil {
		t.Fatal(err)
	}
	rightRaw, err := os.ReadFile(rightPath)
	if err != nil {
		t.Fatal(err)
	}
	var left, right persistentBFTSignerKeyFile
	if err := json.Unmarshal(leftRaw, &left); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rightRaw, &right); err != nil {
		t.Fatal(err)
	}
	left.PublicKey = right.PublicKey
	tampered, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateBFTSigner(leftPath); err == nil {
		t.Fatal("mismatched public and private keys must be rejected")
	}
}

func TestLoadOrCreateBFTSignerNeedsExistingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "validator.json")
	if _, _, err := LoadOrCreateBFTSigner(path); err == nil {
		t.Fatal("missing parent directory should be rejected")
	}
}

func TestLoadOrCreateBFTSignerRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator.json")
	if err := os.WriteFile(path, make([]byte, maxBFTSignerKeyFileSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateBFTSigner(path); err == nil {
		t.Fatal("oversized consensus signer file should be rejected")
	}
}
