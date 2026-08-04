package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
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

func TestLegacyWalletRecoveryMigrationAndRestorePreserveAddress(t *testing.T) {
	dir := t.TempDir()
	walletPath := filepath.Join(dir, "legacy-wallet.json")
	restoredPath := filepath.Join(dir, "restored-wallet.json")
	recoveryPath := filepath.Join(dir, "legacy-recovery.txt")
	exportedPath := filepath.Join(dir, "legacy-recovery-exported.txt")
	oldPassphrase := filepath.Join(dir, "old-passphrase.txt")
	newPassphrase := filepath.Join(dir, "new-passphrase.txt")
	if err := os.WriteFile(oldPassphrase, []byte("old wallet strong passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPassphrase, []byte("restored wallet different passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}

	var (
		mu         sync.Mutex
		registered *chain.RecoveryCapsuleAction
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/wallet/recovery/nonce":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"action_nonce": uint64(0), "present": true,
			})
		case r.URL.Path == "/api/v1/wallet/recovery/capsules/submit-signed":
			var envelope legacyRecoveryEnvelope
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			payload, _ := json.Marshal(envelope.Action)
			tx := &mempool.Tx{
				ID: envelope.Action.ID, Sender: envelope.Action.Sender,
				Nonce: envelope.Action.Nonce, Payload: payload,
				ContractID: chain.RecoveryCapsuleContractID,
				Signature:  envelope.Signature, PublicKey: envelope.PublicKey,
			}
			if err := chain.VerifyRecoveryCapsuleTx(tx); err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			copyAction := envelope.Action
			mu.Lock()
			registered = &copyAction
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
		case strings.HasPrefix(r.URL.Path, "/api/v1/wallet/recovery/capsules/"):
			mu.Lock()
			action := registered
			mu.Unlock()
			if action == nil || !strings.HasSuffix(r.URL.Path, action.Locator) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(legacyRecoveryCapsuleResponse{State: chain.RecoveryCapsuleState{
				Owner: action.Sender, Locator: action.Locator, Capsule: action.Capsule,
				ActionID: action.ID, RegisteredAt: action.Timestamp,
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli := &CLI{baseURL: server.URL + "/api/v1", client: server.Client()}
	if err := cli.walletNew([]string{
		"--out", walletPath,
		"--passphrase-file", oldPassphrase,
	}); err != nil {
		t.Fatalf("create legacy wallet: %v", err)
	}
	before, err := loadKeystore(walletPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cli.walletEnableLegacyRecovery([]string{
		"--in", walletPath,
		"--passphrase-file", oldPassphrase,
		"--recovery-out", recoveryPath,
		"--confirm-timeout", "2s",
	}); err != nil {
		t.Fatalf("enable legacy recovery: %v", err)
	}
	migrated, err := loadKeystore(walletPath)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Address != before.Address || migrated.PublicKey != before.PublicKey {
		t.Fatal("migration changed the legacy wallet identity")
	}
	if migrated.Recovery == nil || migrated.Recovery.Scheme != keystore.LegacyRecoveryScheme {
		t.Fatalf("legacy recovery metadata missing: %+v", migrated.Recovery)
	}
	backups, err := filepath.Glob(walletPath + ".pre-recovery-*.bak")
	if err != nil || len(backups) != 1 {
		t.Fatalf("migration backup count=%d err=%v", len(backups), err)
	}

	if err := cli.walletExportRecovery([]string{
		"--in", walletPath,
		"--out", exportedPath,
		"--passphrase-file", oldPassphrase,
	}); err != nil {
		t.Fatalf("export migrated recovery: %v", err)
	}
	originalWords, _ := os.ReadFile(recoveryPath)
	exportedWords, _ := os.ReadFile(exportedPath)
	if strings.TrimSpace(string(originalWords)) != strings.TrimSpace(string(exportedWords)) {
		t.Fatal("migrated recovery export changed the recovery words")
	}

	if err := cli.walletRestoreLegacy([]string{
		"--out", restoredPath,
		"--recovery-file", recoveryPath,
		"--passphrase-file", newPassphrase,
	}); err != nil {
		t.Fatalf("restore migrated legacy wallet: %v", err)
	}
	restored, err := loadKeystore(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Address != before.Address || restored.PublicKey != before.PublicKey {
		t.Fatalf("restored legacy identity changed: got %s want %s", restored.Address, before.Address)
	}
}
