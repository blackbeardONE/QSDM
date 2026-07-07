package wallet

import (
	"testing"

	"github.com/blackbeardONE/QSDM/wasm_modules/wallet/walletcore"
)

func requireWalletcore(t *testing.T) {
	t.Helper()
	if walletcore.GetAddress() == "" {
		t.Skip("walletcore not initialized (walletcrypto has no backend in this build)")
	}
}

func TestWalletBalance(t *testing.T) {
	requireWalletcore(t)
	if walletcore.GetBalance() != 0 {
		t.Errorf("expected canonical-ledger balance 0, got %d", walletcore.GetBalance())
	}
}

func TestSendTransactionRequiresCanonicalFunds(t *testing.T) {
	requireWalletcore(t)
	_, err := walletcore.SendTransaction("recipient-address", 100, 0, "", nil)
	if err == nil {
		t.Fatal("expected an unfunded wallet to reject the transfer")
	}
	if walletcore.GetBalance() != 0 {
		t.Errorf("failed transfer changed balance: got %d", walletcore.GetBalance())
	}
}

func TestSendTransactionInsufficientFunds(t *testing.T) {
	requireWalletcore(t)
	_, err := walletcore.SendTransaction("recipient-address", 10000, 0, "", nil)
	if err == nil {
		t.Fatal("expected error for insufficient funds")
	}
}

func TestSignAndVerify(t *testing.T) {
	requireWalletcore(t)
	message := []byte("test message")
	signature, err := walletcore.SignTransaction(message)
	if err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}
	keyPair := walletcore.GetKeyPair()
	if keyPair == nil {
		t.Fatal("KeyPair is nil")
	}
	valid, err := keyPair.Verify(message, signature)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !valid {
		t.Fatal("signature verification failed")
	}
}
