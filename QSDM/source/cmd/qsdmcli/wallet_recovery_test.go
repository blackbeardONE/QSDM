package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/keystore"
)

func TestWalletRecoveryCreateRestoreAndExport(t *testing.T) {
	dir := t.TempDir()
	firstWallet := filepath.Join(dir, "first-wallet.json")
	restoredWallet := filepath.Join(dir, "restored-wallet.json")
	recoveryFile := filepath.Join(dir, "recovery.txt")
	exportedRecoveryFile := filepath.Join(dir, "exported-recovery.txt")
	firstPassphrase := filepath.Join(dir, "first-passphrase.txt")
	restoredPassphrase := filepath.Join(dir, "restored-passphrase.txt")
	if err := os.WriteFile(firstPassphrase, []byte("first strong passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restoredPassphrase, []byte("different strong passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}

	cli := &CLI{}
	if err := cli.walletNew([]string{
		"--out", firstWallet,
		"--passphrase-file", firstPassphrase,
		"--recovery-out", recoveryFile,
	}); err != nil {
		t.Fatalf("wallet new: %v", err)
	}
	words, err := os.ReadFile(recoveryFile)
	if err != nil {
		t.Fatalf("read recovery file: %v", err)
	}
	if got := len(strings.Fields(string(words))); got != keystore.RecoveryWords {
		t.Fatalf("recovery word count = %d, want %d", got, keystore.RecoveryWords)
	}

	if err := cli.walletRestore([]string{
		"--out", restoredWallet,
		"--passphrase-file", restoredPassphrase,
		"--recovery-file", recoveryFile,
	}); err != nil {
		t.Fatalf("wallet restore: %v", err)
	}

	first, err := loadKeystore(firstWallet)
	if err != nil {
		t.Fatalf("load first wallet: %v", err)
	}
	restored, err := loadKeystore(restoredWallet)
	if err != nil {
		t.Fatalf("load restored wallet: %v", err)
	}
	if first.Address != restored.Address {
		t.Fatalf("restored address = %s, want %s", restored.Address, first.Address)
	}
	if first.PublicKey != restored.PublicKey {
		t.Fatal("restored public key differs")
	}

	if err := cli.walletExportRecovery([]string{
		"--in", restoredWallet,
		"--out", exportedRecoveryFile,
		"--passphrase-file", restoredPassphrase,
	}); err != nil {
		t.Fatalf("wallet export-recovery: %v", err)
	}
	exportedWords, err := os.ReadFile(exportedRecoveryFile)
	if err != nil {
		t.Fatalf("read exported recovery file: %v", err)
	}
	if strings.TrimSpace(string(exportedWords)) != strings.TrimSpace(string(words)) {
		t.Fatal("exported recovery words differ from the original")
	}
}

func TestWalletRecoveryExportRejectsLegacyWallet(t *testing.T) {
	dir := t.TempDir()
	walletPath := filepath.Join(dir, "legacy-wallet.json")
	passphrasePath := filepath.Join(dir, "passphrase.txt")
	if err := os.WriteFile(passphrasePath, []byte("legacy strong passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}

	cli := &CLI{}
	if err := cli.walletNew([]string{
		"--out", walletPath,
		"--passphrase-file", passphrasePath,
	}); err != nil {
		t.Fatalf("wallet new: %v", err)
	}
	err := cli.walletExportRecovery([]string{
		"--in", walletPath,
		"--out", filepath.Join(dir, "should-not-exist.txt"),
		"--passphrase-file", passphrasePath,
	})
	if err == nil || !strings.Contains(err.Error(), "not created with QSDM Recovery Words") {
		t.Fatalf("expected legacy-wallet recovery error, got %v", err)
	}
}

func TestWalletRecoveryExportRejectsMismatchedPrivateAndRecoveryMaterial(t *testing.T) {
	dir := t.TempDir()
	passphrasePath := filepath.Join(dir, "passphrase.txt")
	if err := os.WriteFile(passphrasePath, []byte("shared strong passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}

	cli := &CLI{}
	createWallet := func(name string) string {
		t.Helper()
		walletPath := filepath.Join(dir, name+".json")
		if err := cli.walletNew([]string{
			"--out", walletPath,
			"--passphrase-file", passphrasePath,
			"--recovery-out", filepath.Join(dir, name+"-recovery.txt"),
		}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return walletPath
	}

	firstPath := createWallet("first")
	secondPath := createWallet("second")
	first, err := loadKeystore(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadKeystore(secondPath)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a metadata/recovery-block swap while retaining the first
	// wallet's encrypted private key. Export must verify all material belongs
	// to one wallet, even when both wallets use the same passphrase.
	first.Address = second.Address
	first.PublicKey = second.PublicKey
	first.Recovery = second.Recovery
	mutated, err := keystore.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	mutatedPath := filepath.Join(dir, "mutated.json")
	if err := os.WriteFile(mutatedPath, mutated, 0o600); err != nil {
		t.Fatal(err)
	}

	err = cli.walletExportRecovery([]string{
		"--in", mutatedPath,
		"--out", filepath.Join(dir, "must-not-export.txt"),
		"--passphrase-file", passphrasePath,
	})
	if err == nil || !strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("expected private/public integrity error, got %v", err)
	}
}
