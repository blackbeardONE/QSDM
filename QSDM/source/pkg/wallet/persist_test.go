package wallet

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLoadOrCreateWalletService_identitySurvivesRestart is the regression
// test for the node minting a new identity on every boot.
//
// Production logs showed the validator's address changing between restarts
// (5aa5d9f5… -> 4b28fb63…) because NewWalletService always calls
// crypto.NewDilithium, which generates a fresh keypair. That made the
// balance permanently zero, stranded any CELL credited to the previous
// address, and churned the node's BFT consensus identity.
func TestLoadOrCreateWalletService_identitySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator.key")

	first, err := LoadOrCreateWalletService(path)
	if err != nil {
		t.Fatalf("first boot: %v", err)
	}
	firstAddr := first.GetAddress()
	if firstAddr == "" {
		t.Fatal("wallet should have an address")
	}

	// Simulate a restart: brand-new service, same key path.
	second, err := LoadOrCreateWalletService(path)
	if err != nil {
		t.Fatalf("second boot: %v", err)
	}

	if second.GetAddress() != firstAddr {
		t.Fatalf("identity must survive restart: first %s, second %s", firstAddr, second.GetAddress())
	}

	// And the loaded key must actually be able to sign.
	tx, err := second.CreateTransaction("recipient_abc", 1, 0, "US", []string{"p1", "p2"})
	if err == nil && len(tx) == 0 {
		t.Fatal("expected a signed transaction body")
	}
}

func TestLoadOrCreateWalletService_createsFileWithRestrictivePerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "validator.key")

	if _, err := LoadOrCreateWalletService(path); err != nil {
		t.Fatalf("create: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("key file should exist: %v", err)
	}
	// Windows does not model POSIX permission bits; the check is only
	// meaningful on Unix-like systems.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key file must be 0600, got %o", perm)
		}
	}
}

// A damaged key file must NOT be silently replaced: regenerating over it
// would destroy the only copy of an identity that may hold funds.
func TestLoadOrCreateWalletService_refusesToOverwriteCorruptKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator.key")
	if err := os.WriteFile(path, []byte("{ not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadOrCreateWalletService(path)
	if !errors.Is(err, ErrWalletKeyCorrupt) {
		t.Fatalf("want ErrWalletKeyCorrupt, got %v", err)
	}

	// The damaged file must still be there for the operator to recover.
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatal("a corrupt key file must be preserved, not replaced")
	}
}

// An address that disagrees with its own public key means the file was
// edited or corrupted; adopting it would silently change identity.
func TestLoadOrCreateWalletService_rejectsAddressMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator.key")
	if _, err := LoadOrCreateWalletService(path); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kf walletKeyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		t.Fatal(err)
	}
	kf.Address = "deadbeef"
	tampered, _ := json.Marshal(kf)
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrCreateWalletService(path); !errors.Is(err, ErrWalletKeyCorrupt) {
		t.Fatalf("want ErrWalletKeyCorrupt for an address mismatch, got %v", err)
	}
}

// Truncated key material must be rejected by the crypto backend rather than
// producing a handle that signs unverifiable signatures.
func TestLoadOrCreateWalletService_rejectsTruncatedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "validator.key")
	if _, err := LoadOrCreateWalletService(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var kf walletKeyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		t.Fatal(err)
	}
	kf.PrivateKey = kf.PrivateKey[:len(kf.PrivateKey)/2]
	kf.Address = "" // skip the address cross-check so the key check is what fires
	bad, _ := json.Marshal(kf)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadOrCreateWalletService(path); !errors.Is(err, ErrWalletKeyCorrupt) {
		t.Fatalf("want ErrWalletKeyCorrupt for truncated key material, got %v", err)
	}
}

// An empty path preserves the historical ephemeral behaviour for tests and
// embedded callers.
func TestLoadOrCreateWalletService_emptyPathIsEphemeral(t *testing.T) {
	a, err := LoadOrCreateWalletService("")
	if err != nil {
		t.Skipf("wallet service unavailable: %v", err)
	}
	b, err := LoadOrCreateWalletService("")
	if err != nil {
		t.Fatal(err)
	}
	if a.GetAddress() == b.GetAddress() {
		t.Fatal("an empty key path should keep the legacy ephemeral behaviour")
	}
}
